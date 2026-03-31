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
	JobID     string
	PrinterID string
	FileName  string
	Status    string
	Copies    int
	Duplex    bool
	User      feishuUserInfo
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

	userIDType, userIDValue := resolveUserIDTypeAndValue(record.User)
	if userIDValue == "" {
		return fmt.Errorf("missing user identity for bitable person field")
	}
	fields[bitableFieldUser] = []map[string]string{{"id": userIDValue}}

	req := larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(s.appToken).
		TableId(s.tableID).
		UserIdType(userIDType).
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

func (s *bitableJobStore) ListJobsByUser(ctx context.Context, user feishuUserInfo, limit int) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if limit <= 0 {
		limit = 200
	}

	jobs := make([]map[string]interface{}, 0)
	pageToken := ""

	for len(jobs) < limit {
		userIDType, _ := resolveUserIDTypeAndValue(user)
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(s.appToken).
			TableId(s.tableID).
			UserIdType(userIDType).
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
	_, expectedID := resolveUserIDTypeAndValue(user)
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
	job := map[string]interface{}{
		"id":           fieldAsInt(fields[bitableFieldID]),
		"printer_id":   fieldAsString(fields[bitableFieldPrinterID]),
		"file_name":    fieldAsString(fields[bitableFieldFileName]),
		"status":       fieldAsString(fields[bitableFieldStatus]),
		"copies":       fieldAsInt(fields[bitableFieldCopies]),
		"duplex":       fieldAsBool(fields[bitableFieldDuplex]),
		"submitted_at": fieldAsString(fields[bitableFieldSubmittedAt]),
	}
	return job
}

func resolveUserIDTypeAndValue(user feishuUserInfo) (string, string) {
	if strings.TrimSpace(user.OpenID) != "" {
		return larkbitable.UserIdTypeCreateAppTableRecordOpenId, strings.TrimSpace(user.OpenID)
	}
	if strings.TrimSpace(user.UserID) != "" {
		return larkbitable.UserIdTypeCreateAppTableRecordUserId, strings.TrimSpace(user.UserID)
	}
	if strings.TrimSpace(user.UnionID) != "" {
		return larkbitable.UserIdTypeCreateAppTableRecordUnionId, strings.TrimSpace(user.UnionID)
	}
	return larkbitable.UserIdTypeCreateAppTableRecordUserId, ""
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
