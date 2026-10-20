package storage

import (
	"context"

	"github.com/bvarnai/git-drev/internal/domain"
)

// MockManager provides a configurable in-memory mock implementing Manager.
type MockManager struct {
	CheckDirExistsFn func(ctx context.Context, host, remotePath string) (bool, error)
	CloneBaselineFn  func(ctx context.Context, host, srcPath, dstPath string) error
	RemoveDirFn      func(ctx context.Context, host, remotePath string) error
	QueryUsageFn     func(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (string, []domain.RemoteRevision, error)
	DownloadFn       func(ctx context.Context, req TransferRequest) error
	UploadFn         func(ctx context.Context, req TransferRequest) error

	Calls []string
}

// CheckDirExists records the call and delegates to CheckDirExistsFn or returns true.
func (m *MockManager) CheckDirExists(ctx context.Context, host, remotePath string) (bool, error) {
	m.Calls = append(m.Calls, "CheckDirExists "+host+" "+remotePath)
	if m.CheckDirExistsFn != nil {
		return m.CheckDirExistsFn(ctx, host, remotePath)
	}
	return true, nil
}

// CloneBaseline records the call and delegates to CloneBaselineFn or returns nil.
func (m *MockManager) CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error {
	m.Calls = append(m.Calls, "CloneBaseline "+host+" "+srcPath+" "+dstPath)
	if m.CloneBaselineFn != nil {
		return m.CloneBaselineFn(ctx, host, srcPath, dstPath)
	}
	return nil
}

// RemoveDir records the call and delegates to RemoveDirFn or returns nil.
func (m *MockManager) RemoveDir(ctx context.Context, host, remotePath string) error {
	m.Calls = append(m.Calls, "RemoveDir "+host+" "+remotePath)
	if m.RemoveDirFn != nil {
		return m.RemoveDirFn(ctx, host, remotePath)
	}
	return nil
}

// QueryUsage records the call and delegates to QueryUsageFn or returns ("0B", nil, nil).
func (m *MockManager) QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (string, []domain.RemoteRevision, error) {
	m.Calls = append(m.Calls, "QueryUsage "+host+" "+basePath)
	if m.QueryUsageFn != nil {
		return m.QueryUsageFn(ctx, host, basePath, opts...)
	}
	return "0B", nil, nil
}

// Download records the call and delegates to DownloadFn or returns nil.
func (m *MockManager) Download(ctx context.Context, req TransferRequest) error {
	m.Calls = append(m.Calls, "Download "+req.Host+" "+req.RemotePath)
	if m.DownloadFn != nil {
		return m.DownloadFn(ctx, req)
	}
	return nil
}

// Upload records the call and delegates to UploadFn or returns nil.
func (m *MockManager) Upload(ctx context.Context, req TransferRequest) error {
	m.Calls = append(m.Calls, "Upload "+req.Host+" "+req.RemotePath)
	if m.UploadFn != nil {
		return m.UploadFn(ctx, req)
	}
	return nil
}

// RsyncGet is an alias for Download for backwards compatibility.
func (m *MockManager) RsyncGet(ctx context.Context, req TransferRequest) error {
	return m.Download(ctx, req)
}

// RsyncPut is an alias for Upload for backwards compatibility.
func (m *MockManager) RsyncPut(ctx context.Context, req TransferRequest) error {
	return m.Upload(ctx, req)
}
