package api

import (
	"context"
	"fmt"
	"goprint/config"
	"strconv"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
)

const (
	bitableFieldID          = "id"
	bitableFieldJobID       = "job_id"
	bitableFieldPrinterID   = "printer_id"
	bitableFieldFileName    = "file_name"
	bitableFieldStatus      = "status"
	bitableFieldCopies      = "copies"
	bitableFieldDuplex      = "duplex"
	bitableFieldDuplexHook  = "duplex_hook"
	bitableFieldUser        = "user"
	bitableFieldSubmittedAt = "submitted_at"
)

type bitableJobStore struct {
	client   *lark.Client
	appToken string
	tableID  string
	timeout  time.Duration
}

type printJobRecord struct {
	JobID      string
	PrinterID  string
	FileName   string
	Status     string
	Copies     int
	Duplex     bool
	DuplexHook string
	User       feishuUserInfo
}

type trackableJob struct {
	JobID     string
	PrinterID string
	Status    string
}

func newBitableJobStore(cfg *config.Config) (*bitableJobStore, error) {
	if !cfg.JobStore.Enabled {
		return nil, fmt.Errorf("job_store is disabled")
	}

	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	appSecret := strings.TrimSpace(cfg.Auth.Feishu.AppSecret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("auth.feishu.app_id/app_secret is required for bitable job store")
	}

	appToken := strings.TrimSpace(cfg.JobStore.Feishu.AppToken)
	tableID := strings.TrimSpace(cfg.JobStore.Feishu.TableID)
	if appToken == "" || tableID == "" {
		return nil, fmt.Errorf("job_store.feishu.app_token/table_id is required")
	}

	timeout, err := time.ParseDuration(cfg.JobStore.Feishu.RequestTimeout)
	if err != nil || timeout <= 0 {
		timeout = 3 * time.Second
	}

	return &bitableJobStore{
		client:   lark.NewClient(appID, appSecret),
		appToken: appToken,
		tableID:  tableID,
		timeout:  timeout,
	}, nil
}

func (s *bitableJobStore) SaveJob(ctx context.Context, record printJobRecord) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	fields := map[string]interface{}{
		bitableFieldJobID:     record.JobID,
		bitableFieldPrinterID: record.PrinterID,
		bitableFieldFileName:  record.FileName,
		bitableFieldStatus:    record.Status,
		bitableFieldCopies:    record.Copies,
		bitableFieldDuplex:    record.Duplex,
	}

	if v := strings.TrimSpace(record.DuplexHook); v != "" {
		fields[bitableFieldDuplexHook] = v
	}

	openID := strings.TrimSpace(record.User.OpenID)
	if openID == "" {
		return fmt.Errorf("missing open_id for bitable person field")
	}
	fields[bitableFieldUser] = []map[string]string{{"id": openID}}

	req := larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(s.appToken).
		TableId(s.tableID).
		UserIdType(larkbitable.UserIdTypeCreateAppTableRecordOpenId).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(fields).
			Build()).
		Build()

	resp, err := s.client.Bitable.AppTableRecord.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("bitable create record failed: %w", err)
	}
	if resp == nil || !resp.Success() {
		if resp == nil {
			return fmt.Errorf("bitable create record failed: empty response")
		}
		return fmt.Errorf("bitable create record failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}

	return nil
}

func (s *bitableJobStore) ListTrackableJobs(ctx context.Context) ([]trackableJob, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	out := make([]trackableJob, 0)
	pageToken := ""

	for {
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(s.appToken).
			TableId(s.tableID).
			PageSize(100)
		if pageToken != "" {
			reqBuilder = reqBuilder.PageToken(pageToken)
		}

		resp, err := s.client.Bitable.AppTableRecord.List(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("bitable list records failed: %w", err)
		}
		if resp == nil || !resp.Success() || resp.Data == nil {
			if resp == nil {
				return nil, fmt.Errorf("bitable list records failed: empty response")
			}
			return nil, fmt.Errorf("bitable list records failed: code=%d msg=%s", resp.Code, resp.Msg)
		}

		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			jobID := strings.TrimSpace(fieldAsString(item.Fields[bitableFieldJobID]))
			printerID := strings.TrimSpace(fieldAsString(item.Fields[bitableFieldPrinterID]))
			status := strings.ToLower(strings.TrimSpace(fieldAsString(item.Fields[bitableFieldStatus])))
			if jobID == "" || printerID == "" {
				continue
			}
			switch status {
			case "pending", "held", "processing", "pending_manual_continue":
				out = append(out, trackableJob{JobID: jobID, PrinterID: printerID, Status: status})
			}
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}

	return out, nil
}

func (s *bitableJobStore) ListJobsByUser(ctx context.Context, user feishuUserInfo, limit int) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if limit <= 0 {
		limit = 200
	}

	jobs := make([]map[string]interface{}, 0)
	pageToken := ""

	for len(jobs) < limit {
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(s.appToken).
			TableId(s.tableID).
			UserIdType(larkbitable.UserIdTypeListAppTableRecordOpenId).
			PageSize(100)
		if pageToken != "" {
			reqBuilder = reqBuilder.PageToken(pageToken)
		}

		resp, err := s.client.Bitable.AppTableRecord.List(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("bitable list records failed: %w", err)
		}
		if resp == nil || !resp.Success() || resp.Data == nil {
			if resp == nil {
				return nil, fmt.Errorf("bitable list records failed: empty response")
			}
			return nil, fmt.Errorf("bitable list records failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
		}

		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			if !isOwnedByUser(item.Fields, user) {
				continue
			}
			jobs = append(jobs, mapRecordToJob(item))
			if len(jobs) >= limit {
				break
			}
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}

	return jobs, nil
}

func isOwnedByUser(fields map[string]interface{}, user feishuUserInfo) bool {
	if fields == nil {
		return false
	}

	personIDs := fieldAsPersonIDs(fields[bitableFieldUser])
	if len(personIDs) == 0 {
		return false
	}
	expectedID := strings.TrimSpace(user.OpenID)
	if expectedID == "" {
		return false
	}
	for _, id := range personIDs {
		if id == expectedID {
			return true
		}
	}
	return false
}

func mapRecordToJob(record *larkbitable.AppTableRecord) map[string]interface{} {
	fields := record.Fields
	submittedAt := fieldAsBitableDateTime(fields[bitableFieldSubmittedAt])
	duplexHook := fieldAsString(fields[bitableFieldDuplexHook])
	hookExpiresAt := ""
	if duplexHook != "" {
		if expiresAt, ok := calcDuplexHookExpiresAt(fields[bitableFieldSubmittedAt]); ok {
			hookExpiresAt = expiresAt.In(time.Local).Format("2006-01-02 15:04")
		}
	}

	job := map[string]interface{}{
		"id":           fieldAsInt(fields[bitableFieldID]),
		"printer_id":   fieldAsString(fields[bitableFieldPrinterID]),
		"file_name":    fieldAsString(fields[bitableFieldFileName]),
		"status":       fieldAsString(fields[bitableFieldStatus]),
		"copies":       fieldAsInt(fields[bitableFieldCopies]),
		"duplex":       fieldAsBool(fields[bitableFieldDuplex]),
		"submitted_at": submittedAt,
		"duplex_hook":  duplexHook,
	}

	if hookExpiresAt != "" {
		job["hook_expires_at"] = hookExpiresAt
	}

	return job
}

func calcDuplexHookExpiresAt(submittedRaw interface{}) (time.Time, bool) {
	submittedAt, ok := fieldAsTime(submittedRaw)
	if !ok {
		return time.Time{}, false
	}
	return submittedAt.Add(getManualDuplexHookTTL()), true
}

func fieldAsTime(v interface{}) (time.Time, bool) {
	switch val := v.(type) {
	case float64:
		return time.UnixMilli(int64(val)).In(time.Local), true
	case int64:
		return time.UnixMilli(val).In(time.Local), true
	case int:
		return time.UnixMilli(int64(val)).In(time.Local), true
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return time.Time{}, false
		}
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.UnixMilli(ms).In(time.Local), true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.In(time.Local), true
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func fieldAsBitableDateTime(v interface{}) string {
	const outputLayout = "2006-01-02 15:04"

	switch val := v.(type) {
	case float64:
		return time.UnixMilli(int64(val)).In(time.Local).Format(outputLayout)
	case int64:
		return time.UnixMilli(val).In(time.Local).Format(outputLayout)
	case int:
		return time.UnixMilli(int64(val)).In(time.Local).Format(outputLayout)
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return ""
		}

		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.UnixMilli(ms).In(time.Local).Format(outputLayout)
		}

		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.In(time.Local).Format(outputLayout)
		}

		return s
	default:
		return ""
	}
}

func fieldAsPersonIDs(v interface{}) []string {
	ids := make([]string, 0)
	switch val := v.(type) {
	case []interface{}:
		for _, item := range val {
			if m, ok := item.(map[string]interface{}); ok {
				id := fieldAsString(m["id"])
				if id != "" {
					ids = append(ids, id)
				}
			}
		}
	case []map[string]interface{}:
		for _, m := range val {
			id := fieldAsString(m["id"])
			if id != "" {
				ids = append(ids, id)
			}
		}
	case []map[string]string:
		for _, m := range val {
			id := strings.TrimSpace(m["id"])
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func fieldAsString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return ""
	}
}

func fieldAsInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(val))
		return n
	default:
		return 0
	}
}

func fieldAsBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(val))
		return b
	default:
		return false
	}
}

// UpdateJobStatus 根据 job_id 更新任务状态
func (s *bitableJobStore) UpdateJobStatus(ctx context.Context, jobID string, newStatus string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// 1. 查询所有记录以找到对应的 job_id
	recordID := ""
	pageToken := ""

	for recordID == "" {
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(s.appToken).
			TableId(s.tableID).
			PageSize(100)
		if pageToken != "" {
			reqBuilder = reqBuilder.PageToken(pageToken)
		}

		resp, err := s.client.Bitable.AppTableRecord.List(ctx, reqBuilder.Build())
		if err != nil {
			return fmt.Errorf("bitable list records failed: %w", err)
		}
		if resp == nil || !resp.Success() || resp.Data == nil {
			return fmt.Errorf("bitable list records failed: code=%d msg=%s", resp.Code, resp.Msg)
		}

		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			if fieldAsString(item.Fields[bitableFieldJobID]) == jobID {
				if item.RecordId != nil {
					recordID = *item.RecordId
				}
				break
			}
		}

		if recordID != "" {
			break
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}

	if recordID == "" {
		return fmt.Errorf("job_id not found in bitable: %s", jobID)
	}

	// 2. 更新记录的状态字段
	fields := map[string]interface{}{
		bitableFieldStatus: newStatus,
	}

	req := larkbitable.NewUpdateAppTableRecordReqBuilder().
		AppToken(s.appToken).
		TableId(s.tableID).
		RecordId(recordID).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(fields).
			Build()).
		Build()

	updateResp, err := s.client.Bitable.AppTableRecord.Update(ctx, req)
	if err != nil {
		return fmt.Errorf("bitable update record failed: %w", err)
	}
	if updateResp == nil || !updateResp.Success() {
		if updateResp == nil {
			return fmt.Errorf("bitable update record failed: empty response")
		}
		return fmt.Errorf("bitable update record failed: code=%d msg=%s", updateResp.Code, updateResp.Msg)
	}

	return nil
}

// DeleteJobByUserAndJobID 根据当前用户和 job_id 删除多维表中的任务记录。
func (s *bitableJobStore) DeleteJobByUserAndJobID(ctx context.Context, user feishuUserInfo, jobID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return false, fmt.Errorf("job_id is required")
	}

	recordID := ""
	pageToken := ""

	for recordID == "" {
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(s.appToken).
			TableId(s.tableID).
			UserIdType(larkbitable.UserIdTypeListAppTableRecordOpenId).
			PageSize(100)
		if pageToken != "" {
			reqBuilder = reqBuilder.PageToken(pageToken)
		}

		resp, err := s.client.Bitable.AppTableRecord.List(ctx, reqBuilder.Build())
		if err != nil {
			return false, fmt.Errorf("bitable list records failed: %w", err)
		}
		if resp == nil || !resp.Success() || resp.Data == nil {
			if resp == nil {
				return false, fmt.Errorf("bitable list records failed: empty response")
			}
			return false, fmt.Errorf("bitable list records failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
		}

		for _, item := range resp.Data.Items {
			if item == nil || item.Fields == nil {
				continue
			}
			if !isOwnedByUser(item.Fields, user) {
				continue
			}
			if fieldAsString(item.Fields[bitableFieldJobID]) != jobID {
				continue
			}
			if item.RecordId != nil {
				recordID = *item.RecordId
			}
			break
		}

		if recordID != "" {
			break
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}

	if recordID == "" {
		return false, nil
	}

	delReq := larkbitable.NewDeleteAppTableRecordReqBuilder().
		AppToken(s.appToken).
		TableId(s.tableID).
		RecordId(recordID).
		Build()

	delResp, err := s.client.Bitable.AppTableRecord.Delete(ctx, delReq)
	if err != nil {
		return false, fmt.Errorf("bitable delete record failed: %w", err)
	}
	if delResp == nil || !delResp.Success() {
		if delResp == nil {
			return false, fmt.Errorf("bitable delete record failed: empty response")
		}
		return false, fmt.Errorf("bitable delete record failed: code=%d msg=%s request_id=%s", delResp.Code, delResp.Msg, delResp.RequestId())
	}

	return true, nil
}
