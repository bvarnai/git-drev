package storage

import (
	"context"
	"fmt"

	"github.com/bvarnai/git-drev/internal/domain"
)

// MockManager provides a configurable in-memory mock implementing Manager.
type MockManager struct {
	HasRevisionFn    func(ctx context.Context, host, basePath, uuid string) (bool, error)
	CloneBaselineFn  func(ctx context.Context, host, basePath, srcUUID, dstUUID string) error
	RemoveRevisionFn func(ctx context.Context, host, basePath, uuid string) error
	QueryUsageFn     func(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (string, []domain.RemoteRevision, error)
	DownloadFn       func(ctx context.Context, req TransferRequest) error
	UploadFn         func(ctx context.Context, req TransferRequest) error

	Calls []string
}

// HasRevision records the call and delegates to HasRevisionFn or returns true.
func (m *MockManager) HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error) {
	m.Calls = append(m.Calls, fmt.Sprintf("HasRevision %s %s %s", host, basePath, uuid))
	if m.HasRevisionFn != nil {
		return m.HasRevisionFn(ctx, host, basePath, uuid)
	}
	return true, nil
}

// CloneBaseline records the call and delegates to CloneBaselineFn or returns nil.
func (m *MockManager) CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
	m.Calls = append(m.Calls, fmt.Sprintf("CloneBaseline %s %s %s %s", host, basePath, srcUUID, dstUUID))
	if m.CloneBaselineFn != nil {
		return m.CloneBaselineFn(ctx, host, basePath, srcUUID, dstUUID)
	}
	return nil
}

// RemoveRevision records the call and delegates to RemoveRevisionFn or returns nil.
func (m *MockManager) RemoveRevision(ctx context.Context, host, basePath, uuid string) error {
	m.Calls = append(m.Calls, fmt.Sprintf("RemoveRevision %s %s %s", host, basePath, uuid))
	if m.RemoveRevisionFn != nil {
		return m.RemoveRevisionFn(ctx, host, basePath, uuid)
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
	m.Calls = append(m.Calls, fmt.Sprintf("Download %s %s %s", req.Host, req.BasePath, req.UUID))
	if m.DownloadFn != nil {
		return m.DownloadFn(ctx, req)
	}
	return nil
}

// Upload records the call and delegates to UploadFn or returns nil.
func (m *MockManager) Upload(ctx context.Context, req TransferRequest) error {
	m.Calls = append(m.Calls, fmt.Sprintf("Upload %s %s %s", req.Host, req.BasePath, req.UUID))
	if m.UploadFn != nil {
		return m.UploadFn(ctx, req)
	}
	return nil
}
