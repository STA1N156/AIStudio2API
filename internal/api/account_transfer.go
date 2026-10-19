package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

const maxAccountImportBytes int64 = 32 << 20
const maxAccountFileBytes int64 = 1 << 20
const maxAccountTransferCount = 1000

type AccountExportService interface {
	ExportAccountState(context.Context, string) (AccountStateInput, error)
}

type accountImportFile struct {
	name string
	data []byte
}

type accountImportResult struct {
	File    string `json:"file"`
	Account string `json:"account,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// accountImportFiles 在内存中读取 JSON，ZIP 不落盘，并限制解压后的总量。
func accountImportFiles(r *http.Request) ([]accountImportFile, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("请上传 JSON 或 ZIP 文件")
	}
	var files []accountImportFile
	var expanded int64
	add := func(name string, source io.Reader) error {
		if len(files) >= maxAccountTransferCount {
			return fmt.Errorf("一次最多导入 %d 个账号文件", maxAccountTransferCount)
		}
		data, err := io.ReadAll(io.LimitReader(source, maxAccountFileBytes+1))
		expanded += int64(len(data))
		if err != nil {
			return err
		}
		if int64(len(data)) > maxAccountFileBytes || expanded > maxAccountImportBytes {
			return fmt.Errorf("账号 JSON 不得超过 1 MiB，解压后总量不得超过 32 MiB")
		}
		files = append(files, accountImportFile{name: name, data: data})
		return nil
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := part.FileName()
		if name == "" {
			part.Close()
			continue
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".json":
			err = add(name, part)
		case ".zip":
			var data []byte
			data, err = io.ReadAll(io.LimitReader(part, maxAccountImportBytes+1))
			if err == nil && int64(len(data)) > maxAccountImportBytes {
				err = fmt.Errorf("ZIP 不得超过 32 MiB")
			}
			if err == nil {
				var archive *zip.Reader
				archive, err = zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err == nil && len(archive.File) > maxAccountTransferCount {
					err = fmt.Errorf("ZIP 最多包含 %d 个文件", maxAccountTransferCount)
				}
				if err == nil {
					for _, entry := range archive.File {
						if entry.FileInfo().IsDir() || !strings.EqualFold(path.Ext(entry.Name), ".json") {
							continue
						}
						if path.Clean(entry.Name) != entry.Name || path.IsAbs(entry.Name) || strings.Contains(entry.Name, "\\") || strings.HasPrefix(entry.Name, "../") {
							err = fmt.Errorf("ZIP 含有不安全的文件路径")
							break
						}
						var source io.ReadCloser
						source, err = entry.Open()
						if err == nil {
							err = add(name+"/"+entry.Name, source)
							source.Close()
						}
						if err != nil {
							break
						}
					}
				}
			}
		default:
			err = fmt.Errorf("只支持 JSON 或 ZIP 文件")
		}
		part.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("没有找到账号 JSON 文件")
	}
	return files, nil
}

func (s *server) handleImportAccounts(w http.ResponseWriter, r *http.Request) {
	files, err := accountImportFiles(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_account_archive", err.Error())
		return
	}
	results := make([]accountImportResult, len(files))
	inputs := make([]AccountStateInput, len(files))
	var pending []int
	seen := make(map[string]bool)
	for index, file := range files {
		input := &inputs[index]
		result := &results[index]
		*result = accountImportResult{File: file.name, Status: "failed"}
		err := json.Unmarshal(file.data, input)
		if err == nil && input.StorageState.Cookies == nil {
			err = json.Unmarshal(file.data, &input.StorageState)
		}
		if err == nil {
			extension, exists, extensionErr := input.StorageState.AuthExtension()
			err = extensionErr
			if exists && extension.Source.Email != "" {
				input.Label = extension.Source.Email
			}
		}
		if input.Label == "" {
			input.Label = strings.TrimSuffix(path.Base(file.name), path.Ext(file.name))
		}
		input.Label = strings.ToLower(strings.TrimSpace(input.Label))
		result.Account = input.Label
		if err != nil {
			result.Error = "账号 JSON 格式无效"
			continue
		}
		if seen[input.Label] {
			result.Status, result.Error = "skipped", "同一批次中账号重复"
			continue
		}
		seen[input.Label] = true
		pending = append(pending, index)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(3, len(pending)) {
		workers.Go(func() {
			for index := range jobs {
				result := &results[index]
				account, err := s.config.Admin.ImportAccountState(r.Context(), inputs[index])
				if err == nil {
					result.Status = "imported"
					result.Account = account.ID
				} else {
					result.Error = err.Error()
					if statusFromError(err) == http.StatusConflict {
						result.Status = "skipped"
					}
				}
			}
		})
	}
	for _, index := range pending {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *server) handleExportAccounts(w http.ResponseWriter, r *http.Request) {
	exporter, ok := s.config.Admin.(AccountExportService)
	if !ok {
		writeAdminError(w, http.StatusNotImplemented, "export_unavailable", "当前服务不支持账号导出")
		return
	}
	ids := []string{r.PathValue("id")}
	single := r.Method == http.MethodGet
	if !single {
		var input struct {
			IDs []string `json:"ids"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "请选择要导出的账号")
			return
		}
		ids = input.IDs
	}
	if len(ids) == 0 || len(ids) > maxAccountTransferCount {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "请选择 1 至 1000 个账号")
		return
	}
	var files []accountImportFile
	var exportedBytes int64
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		account, err := exporter.ExportAccountState(r.Context(), id)
		if err != nil {
			writeAdminUpstreamError(w, err)
			return
		}
		data, err := json.MarshalIndent(account, "", "  ")
		if err != nil {
			writeAdminUpstreamError(w, err)
			return
		}
		exportedBytes += int64(len(data))
		if int64(len(data)) > maxAccountFileBytes || exportedBytes > maxAccountImportBytes {
			writeAdminError(w, http.StatusBadRequest, "export_too_large", "导出数据超过 32 MiB，请减少选中账号")
			return
		}
		files = append(files, accountImportFile{name: path.Base(account.Label) + ".json", data: data})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if single {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": files[0].name}))
		w.Write(files[0].data)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="aistudio-accounts.zip"`)
	archive := zip.NewWriter(w)
	for _, file := range files {
		entry, err := archive.Create(file.name)
		if err != nil {
			break
		}
		if _, err := entry.Write(file.data); err != nil {
			break
		}
	}
	archive.Close()
}
