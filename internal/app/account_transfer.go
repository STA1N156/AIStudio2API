package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// ExportAccountState 只读取原子保存的认证文件，不等待或打断正在生成的请求。
func (admin *runtimeAdmin) ExportAccountState(ctx context.Context, id string) (api.AccountStateInput, error) {
	if err := ctx.Err(); err != nil {
		return api.AccountStateInput{}, err
	}
	summary, err := admin.account(id)
	if err != nil {
		return api.AccountStateInput{}, err
	}
	account, err := admin.pool.Account(id)
	if err != nil {
		return api.AccountStateInput{}, err
	}
	state, err := aistudio.LoadStorageState(account.StoragePath)
	if err != nil {
		return api.AccountStateInput{}, err
	}
	fingerprint, err := os.ReadFile(filepath.Join(account.Directory, "camoufox-fingerprint.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return api.AccountStateInput{}, err
	}
	return api.AccountStateInput{
		Label: summary.ID, Proxy: summary.Proxy, Enabled: &summary.Enabled,
		Locale: summary.Locale, Timezone: summary.Timezone, StorageState: state, Fingerprint: fingerprint,
	}, nil
}

func (manager *runtimeManager) ExportAccountState(ctx context.Context, id string) (api.AccountStateInput, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.(api.AccountExportService).ExportAccountState(ctx, id)
}
