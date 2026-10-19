// Account checker: isolated Playground tests, with optional backed-up deletion.
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

type accountConfig struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

type account struct {
	accountConfig
	ID string `json:"id"`
}

type exportedAccount struct {
	accountConfig
	StorageState aistudio.StorageState `json:"storage_state"`
	Fingerprint  json.RawMessage       `json:"fingerprint"`
}

type attempt struct {
	Kind    string  `json:"kind"`
	Stage   string  `json:"stage"`
	Status  int     `json:"http_status,omitempty"`
	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
}

type result struct {
	Account  string    `json:"account"`
	Kind     string    `json:"kind"`
	Attempts []attempt `json:"attempts"`
}

type panel struct {
	base   string
	client *http.Client
}

func (p *panel) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", p.base)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not print response bodies: they may contain credentials.
		return fmt.Errorf("面板 %s %s 返回 HTTP %d", method, path, resp.StatusCode)
	}
	if data, ok := output.(*[]byte); ok {
		*data, err = io.ReadAll(io.LimitReader(resp.Body, 33<<20))
		return err
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

func (p *panel) accounts(ctx context.Context) (map[string]account, error) {
	var response struct {
		Accounts []account `json:"accounts"`
	}
	if err := p.call(ctx, "GET", "/api/accounts", nil, &response); err != nil {
		return nil, err
	}
	accounts := make(map[string]account, len(response.Accounts))
	for _, item := range response.Accounts {
		accounts[item.ID] = item
	}
	return accounts, nil
}

// Validate the entire backup before using it or allowing deletion.
func decodeBackup(data []byte, ids []string) (map[string]exportedAccount, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	expected := make(map[string]bool, len(ids))
	for _, id := range ids {
		expected[id] = true
	}
	if len(archive.File) != len(expected) {
		return nil, errors.New("备份账号数量不符，已停止")
	}
	items := make(map[string]exportedAccount, len(ids))
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > 1<<20 {
			return nil, errors.New("备份中单个账号文件过大")
		}
		r, err := entry.Open()
		if err != nil {
			return nil, err
		}
		var item exportedAccount
		err = json.NewDecoder(io.LimitReader(r, (1<<20)+1)).Decode(&item)
		r.Close()
		if err != nil || !expected[item.Label] || len(item.StorageState.Cookies) == 0 {
			return nil, errors.New("备份中存在无效或不匹配的账号")
		}
		if _, exists := items[item.Label]; exists {
			return nil, errors.New("备份中存在重复账号")
		}
		items[item.Label] = item
	}
	return items, nil
}

func (p *panel) backup(ctx context.Context, ids []string, dir, prefix string) (map[string]exportedAccount, error) {
	items := make(map[string]exportedAccount, len(ids))
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		var data []byte
		if err := p.call(ctx, "POST", "/api/accounts/export", map[string]any{"ids": batch}, &data); err != nil {
			return nil, err
		}
		decoded, err := decodeBackup(data, batch)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%03d.zip", prefix, start/100+1))
		if err := os.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		for id, item := range decoded {
			items[id] = item
		}
	}
	return items, nil
}

func selectIDs(all map[string]account, text string, selectAll bool) ([]string, error) {
	seen := make(map[string]bool)
	if selectAll {
		for id := range all {
			seen[id] = true
		}
	} else {
		for _, line := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
			id := strings.TrimSpace(line)
			if id == "" || strings.HasPrefix(id, "#") {
				continue
			}
			if _, ok := all[id]; !ok {
				return nil, fmt.Errorf("面板中没有账号 %q；请每行只写一个完整账号 ID", id)
			}
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("没有选中账号")
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func errorKind(err error) (string, int) {
	var access *aistudio.ModelAccessError
	if errors.As(err, &access) {
		return "no_qualification", access.HTTPStatus()
	}
	var rpc *aistudio.RPCError
	if errors.As(err, &rpc) {
		if rpc.StatusCode == 403 {
			return "forbidden", 403
		}
		return "other", rpc.StatusCode
	}
	return "other", 0
}

// Proxy URLs may occur in network errors. Never write their credentials to reports.
func safeError(err error, proxy string) string {
	message := err.Error()
	if proxy != "" {
		message = strings.ReplaceAll(message, proxy, "[账号代理]")
		if u, e := url.Parse(proxy); e == nil && u.User != nil {
			message = strings.ReplaceAll(message, u.User.String()+"@", "[已隐藏]@")
			if password, ok := u.User.Password(); ok && password != "" {
				message = strings.ReplaceAll(message, password, "[已隐藏]")
			}
		}
	}
	return message
}

func probe(ctx context.Context, item exportedAccount, model, fallbackProxy string, timeout time.Duration) (out attempt) {
	started := time.Now()
	out.Kind, out.Stage = "other", "setup"
	proxy := item.Proxy
	if proxy == "" {
		proxy = fallbackProxy
	}
	defer func() { out.Seconds = time.Since(started).Seconds() }()
	fail := func(err error) attempt {
		out.Kind, out.Status = errorKind(err)
		out.Error = safeError(err, proxy)
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Every attempt has its own directory and one-account pool. No live state is edited.
	dir, err := os.MkdirTemp("", "aistudio-account-check-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(dir)
	dir = filepath.Join(dir, "account")
	if err := os.Mkdir(dir, 0700); err != nil {
		return fail(err)
	}
	config := item.accountConfig
	config.Enabled, config.Proxy = true, proxy
	for name, value := range map[string]any{
		"account.json": config, "storage-state.json": item.StorageState,
	} {
		data, err := json.Marshal(value)
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return fail(err)
		}
	}
	if len(item.Fingerprint) > 0 && string(item.Fingerprint) != "null" {
		if err := os.WriteFile(filepath.Join(dir, "camoufox-fingerprint.json"), item.Fingerprint, 0600); err != nil {
			return fail(err)
		}
	}
	accounts, err := aistudio.NewAccountStore(dir).Load()
	if err != nil || len(accounts) != 1 {
		if err == nil {
			err = errors.New("隔离副本没有载入唯一账号")
		}
		return fail(err)
	}
	pool := aistudio.NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]aistudio.Channel{aistudio.ChannelPlayground})
	httpClient, err := aistudio.NewProxyHTTPClient(proxy)
	if err != nil {
		return fail(err)
	}
	defer httpClient.CloseIdleConnections()
	headers, err := aistudio.DiscoverPublicHeaders(ctx, httpClient)
	if err != nil {
		return fail(err)
	}
	transport, err := aistudio.NewMakerSuiteHTTPTransport(aistudio.HTTPTransportOptions{
		Pool: pool, GlobalProxy: proxy,
		Headers: aistudio.ProtocolHeaderProviderFunc(func(context.Context, string) (http.Header, error) {
			return headers.Clone(), nil
		}),
	})
	if err != nil {
		return fail(err)
	}
	defer transport.CloseIdleConnections()
	var worker *aistudio.NativeWorker
	defer func() {
		if worker != nil {
			worker.Close()
		}
	}()
	protected, err := aistudio.NewWorkerProtectedTransport(aistudio.WorkerProtectedTransportOptions{
		Transport: transport, SetupTimeout: timeout,
		Workers: aistudio.ProtectedPreparerProviderFunc(func(ctx context.Context, id, model string) (aistudio.ProtectedPreparer, error) {
			if worker == nil {
				var err error
				worker, err = aistudio.NewGoWorker(ctx, id, camoufoxnative.Options{
					StorageStatePath: accounts[0].StoragePath, Model: model,
					Proxy: proxy, Locale: config.Locale, Timezone: config.Timezone,
					Headless: true, TemporaryChat: true, ReadyTimeout: timeout, Log: io.Discard,
				})
				if err != nil {
					return nil, err
				}
			}
			return worker, nil
		}),
	})
	if err != nil {
		return fail(err)
	}
	provider, err := aistudio.NewPoolRequestContextProvider(pool)
	if err != nil {
		return fail(err)
	}
	client, err := aistudio.NewClient(aistudio.ClientOptions{Transport: transport, Protected: protected, ContextProvider: provider})
	if err != nil {
		return fail(err)
	}
	service, err := aistudio.NewPooledService(pool, client)
	if err != nil {
		return fail(err)
	}
	out.Stage = "catalog"
	if _, err = service.RefreshAccountModels(ctx, accounts[0].ID); err != nil {
		return fail(err)
	}
	if pool.Status()[0].BenefitTier == "Free" {
		out.Kind, out.Error = "no_qualification", "权益查询确认账号为 Free"
		return out
	}
	out.Stage = "generation"
	limit := int64(128)
	model, generationConfig, _ := aistudio.ApplyModelSuffix(model, aistudio.GenerationConfig{MaxOutputTokens: &limit, ReasoningEffort: "low"})
	events, err := service.Generate(ctx, aistudio.GenerateRequest{
		ID: fmt.Sprintf("account-check-%d", time.Now().UnixNano()), Model: model, AccountID: accounts[0].ID,
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "Reply only OK."}}}},
		Config:   generationConfig,
	})
	if err != nil {
		return fail(err)
	}
	var text strings.Builder
	for event := range events {
		if event.Kind == aistudio.EventError {
			if event.Err == nil {
				return fail(errors.New("上游返回未说明原因的错误"))
			}
			return fail(event.Err)
		}
		if event.Kind == aistudio.EventText {
			text.WriteString(event.Text)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if strings.TrimSpace(text.String()) == "" {
		return fail(errors.New("输出为空，保留待查，不当作无资格删除"))
	}
	out.Kind, out.Status = "ok", 200
	return out
}

func check(ctx context.Context, item exportedAccount, model, proxy string, timeout time.Duration) result {
	first := probe(ctx, item, model, proxy, timeout)
	r := result{Account: item.Label, Kind: first.Kind, Attempts: []attempt{first}}
	// A single failure never authorizes deletion. Independently confirm bad accounts once.
	if first.Kind == "forbidden" || first.Kind == "no_qualification" {
		second := probe(ctx, item, model, proxy, timeout)
		r.Attempts = append(r.Attempts, second)
		if second.Kind != first.Kind {
			r.Kind = "other"
			if second.Kind == "ok" {
				r.Kind = "ok"
			}
		}
	}
	return r
}

func writeReports(dir, model string, results []result) error {
	data, err := json.MarshalIndent(map[string]any{"model": model, "results": results}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), data, 0600); err != nil {
		return err
	}
	for _, kind := range []string{"ok", "forbidden", "no_qualification", "other"} {
		var ids []string
		for _, r := range results {
			if r.Kind == kind {
				ids = append(ids, r.Account)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, kind+".txt"), []byte(strings.Join(ids, "\n")), 0600); err != nil {
			return err
		}
	}
	return nil
}

func run(ctx context.Context) error {
	base := flag.String("url", "", "AIStudio2API 面板地址，不加 /v1")
	user := flag.String("user", "admin", "面板管理员账号")
	list := flag.String("accounts", "", "指定账号 TXT，每行一个完整账号 ID")
	all := flag.Bool("all", false, "明确选择面板全部账号，不能与 -accounts 同用")
	model := flag.String("model", "gemini-4-argon", "实际测试的模型")
	parallel := flag.Int("concurrency", 25, "并发账号数")
	timeout := flag.Duration("timeout", 180*time.Second, "每次测试的总超时")
	fallbackProxy := flag.String("proxy", "", "没有单账号代理时使用的备用代理")
	remove := flag.Bool("delete", false, "检测后询问是否备份并删除已复核的 403/无资格账号")
	output := flag.String("out", ".", "报告及备份保存目录")
	flag.Parse()
	u, err := url.Parse(strings.TrimRight(*base, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("请用 -url 填写面板根地址，例如 https://example.com，不加 /v1")
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return errors.New("远程面板必须使用 HTTPS，避免泄露密码和账号")
	}
	if *parallel < 1 || *parallel > 100 || *timeout <= 0 || (*all == (*list != "")) {
		return errors.New("并发需为 1–100；超时需大于 0；-accounts 文件和 -all 必须二选一")
	}
	password := os.Getenv("AISTUDIO_ADMIN_PASSWORD")
	if password == "" {
		return errors.New("请设置 AISTUDIO_ADMIN_PASSWORD，或使用随包的启动脚本输入密码")
	}
	jar, _ := cookiejar.New(nil)
	p := &panel{base: strings.TrimRight(*base, "/"), client: &http.Client{
		Jar: jar, Timeout: 90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if err := p.call(ctx, "POST", "/api/auth/login", map[string]string{"username": *user, "password": password}, nil); err != nil {
		return err
	}
	defer func() { _ = p.call(context.Background(), "POST", "/api/auth/logout", nil, nil) }()
	snapshot, err := p.accounts(ctx)
	if err != nil {
		return err
	}
	var text []byte
	if !*all {
		text, err = os.ReadFile(*list)
		if err != nil {
			return err
		}
	}
	ids, err := selectIDs(snapshot, string(text), *all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*output, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(*output, "account-check-"+time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return err
	}
	dir, _ = filepath.Abs(dir)
	fmt.Printf("指定 %d 个账号；并发 %d；模型 %s\n先导出并校验完整备份……\n", len(ids), min(*parallel, len(ids)), *model)
	items, err := p.backup(ctx, ids, dir, "accounts-backup")
	if err != nil {
		return err
	}
	jobs, done := make(chan string), make(chan result, len(ids))
	var workers sync.WaitGroup
	for i := 0; i < min(*parallel, len(ids)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				done <- check(ctx, items[id], *model, *fallbackProxy, *timeout)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(done) }()
	results := make([]result, 0, len(ids))
	counts := map[string]int{}
	for r := range done {
		results = append(results, r)
		counts[r.Kind]++
		fmt.Printf("[%d/%d] %-18s %s\n", len(results), len(ids), r.Kind, r.Account)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Account < results[j].Account })
	if err := writeReports(dir, *model, results); err != nil {
		return err
	}
	fmt.Printf("正常 %d；403 %d；无模型资格 %d；其他待查 %d\n报告和备份：%s\n", counts["ok"], counts["forbidden"], counts["no_qualification"], counts["other"], dir)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var bad []string
	for _, r := range results {
		if r.Kind == "forbidden" || r.Kind == "no_qualification" {
			bad = append(bad, r.Account)
		}
	}
	if !*remove || len(bad) == 0 {
		fmt.Println("未修改或删除面板账号。")
		return nil
	}
	fmt.Printf("将删除以上 %d 个连续两次确认的 403/无资格账号。无资格仅指 %s，不代表其他模型不可用。\n输入 DELETE 确认，其他输入取消：", len(bad), *model)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(answer) != "DELETE" {
		fmt.Println("已取消删除。")
		return nil
	}
	current, err := p.accounts(ctx)
	if err != nil {
		return err
	}
	for _, id := range bad {
		if current[id] != snapshot[id] {
			return fmt.Errorf("账号 %s 配置已变化或已删除，为防误删已停止，请重新检测", id)
		}
	}
	if _, err := p.backup(ctx, bad, dir, "before-delete"); err != nil {
		return fmt.Errorf("删除前备份失败，未删除：%w", err)
	}
	var deleted, failed []string
	for _, id := range bad {
		if err := p.call(ctx, "DELETE", "/api/accounts/"+url.PathEscape(id), nil, nil); err != nil {
			fmt.Printf("删除未成功 %s：%v\n", id, err)
			failed = append(failed, id)
		} else {
			deleted = append(deleted, id)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "deleted.txt"), []byte(strings.Join(deleted, "\n")), 0600); err != nil {
		return err
	}
	remaining, err := p.accounts(ctx)
	if err != nil {
		return fmt.Errorf("删除后读取面板失败，请手工核对；记录在 deleted.txt：%w", err)
	}
	for _, id := range deleted {
		if _, exists := remaining[id]; exists {
			return fmt.Errorf("删除后账号 %s 仍存在，请查看面板", id)
		}
	}
	for id := range snapshot {
		if _, exists := remaining[id]; !exists && !contains(deleted, id) {
			return fmt.Errorf("未列入删除的账号 %s 已不存在，请核查其他管理员操作", id)
		}
	}
	fmt.Printf("删除并核对完成：成功 %d，未成功 %d。备份可重新导入恢复。\n", len(deleted), len(failed))
	if len(failed) > 0 {
		return errors.New("部分账号未删除；可能正在使用，请查看面板，不会强行中断请求")
	}
	return nil
}

func contains(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "停止：", err)
		os.Exit(1)
	}
}
