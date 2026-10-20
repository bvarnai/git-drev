package transport

import (
	"context"

	"github.com/bvarnai/git-drev/internal/domain"
)

// MockRemoteRunner provides a configurable in-memory mock implementing RemoteRunner.
type MockRemoteRunner struct {
	CheckDirExistsFn func(ctx context.Context, host, remotePath string) (bool, error)
	CloneBaselineFn  func(ctx context.Context, host, srcPath, dstPath string) error
	RemoveDirFn      func(ctx context.Context, host, remotePath string) error
	QueryUsageFn     func(ctx context.Context, host, basePath string) (string, []domain.RemoteRevision, error)
	RsyncGetFn       func(ctx context.Context, req SyncRequest) error
	RsyncPutFn       func(ctx context.Context, req SyncRequest) error

	Calls []string
}

// CheckDirExists records the call and delegates to CheckDirExistsFn or returns true.
func (m *MockRemoteRunner) CheckDirExists(ctx context.Context, host, remotePath string) (bool, error) {
	m.Calls = append(m.Calls, "CheckDirExists "+host+" "+remotePath)
	if m.CheckDirExistsFn != nil {
		return m.CheckDirExistsFn(ctx, host, remotePath)
	}
	return true, nil
}

// CloneBaseline records the call and delegates to CloneBaselineFn or returns nil.
func (m *MockRemoteRunner) CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error {
	m.Calls = append(m.Calls, "CloneBaseline "+host+" "+srcPath+" "+dstPath)
	if m.CloneBaselineFn != nil {
		return m.CloneBaselineFn(ctx, host, srcPath, dstPath)
	}
	return nil
}

// RemoveDir records the call and delegates to RemoveDirFn or returns nil.
func (m *MockRemoteRunner) RemoveDir(ctx context.Context, host, remotePath string) error {
	m.Calls = append(m.Calls, "RemoveDir "+host+" "+remotePath)
	if m.RemoveDirFn != nil {
		return m.RemoveDirFn(ctx, host, remotePath)
	}
	return nil
}

// QueryUsage records the call and delegates to QueryUsageFn or returns ("0B", nil, nil).
func (m *MockRemoteRunner) QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (string, []domain.RemoteRevision, error) {
	m.Calls = append(m.Calls, "QueryUsage "+host+" "+basePath)
	if m.QueryUsageFn != nil {
		return m.QueryUsageFn(ctx, host, basePath)
	}
	return "0B", nil, nil
}

// RsyncGet records the call and delegates to RsyncGetFn or returns nil.
func (m *MockRemoteRunner) RsyncGet(ctx context.Context, req SyncRequest) error {
	m.Calls = append(m.Calls, "RsyncGet "+req.Host+" "+req.RemotePath)
	if m.RsyncGetFn != nil {
		return m.RsyncGetFn(ctx, req)
	}
	return nil
}

// RsyncPut records the call and delegates to RsyncPutFn or returns nil.
func (m *MockRemoteRunner) RsyncPut(ctx context.Context, req SyncRequest) error {
	m.Calls = append(m.Calls, "RsyncPut "+req.Host+" "+req.RemotePath)
	if m.RsyncPutFn != nil {
		return m.RsyncPutFn(ctx, req)
	}
	return nil
}
