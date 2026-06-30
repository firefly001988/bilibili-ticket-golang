package cluster_service

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/payqr"
)

const maxOrderRecords = 1000

func (s *ClusterService) saveOrderRecord(intent domain.LogicalOrderIntent, result domain.ExecutionResult) (domain.OrderRecord, error) {
	if result.OrderID == "" && result.PaymentURL == "" {
		return domain.OrderRecord{}, fmt.Errorf("order result has neither order id nor payment url")
	}
	recordID := result.AttemptID
	if recordID == "" {
		recordID = intent.ID
	}
	if result.OrderID != "" {
		recordID += ":" + result.OrderID
	}
	record := domain.OrderRecord{
		ID:            recordID,
		OrderID:       result.OrderID,
		AttemptID:     result.AttemptID,
		IntentID:      intent.ID,
		MacroTaskID:   intent.MacroTaskID,
		PaymentURL:    result.PaymentURL,
		PaymentExpire: result.PaymentExpire,
		OrderTime:     result.OrderTime,
		CreatedAt:     time.Now(),
	}
	for _, buyer := range intent.Buyers {
		if buyer.Name != "" {
			record.BuyerNames = append(record.BuyerNames, buyer.Name)
		}
	}
	if result.FinishedAt.IsZero() {
		if !result.StartedAt.IsZero() {
			record.CreatedAt = result.StartedAt
		}
	} else {
		record.CreatedAt = result.FinishedAt
	}
	ctx := context.Background()
	if s.repository != nil {
		if macros, err := s.repository.ListMacroTasks(ctx); err == nil {
			for _, macro := range macros {
				if macro.ID != intent.MacroTaskID {
					continue
				}
				record.TaskGroupID = macro.TaskGroupID
				record.ProjectID = macro.ProjectID
				record.ProjectName = macro.ProjectName
				record.ScreenID = macro.ScreenID
				record.ScreenName = macro.ScreenName
				record.SKUID = macro.SKUID
				record.SKUName = macro.SKUName
				break
			}
		}
	}
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.ID == result.AttemptID {
			record.AccountID = attempt.AccountID
			record.WorkerID = attempt.WorkerID
			break
		}
	}
	if (record.AccountID == "" || record.WorkerID == "") && s.repository != nil {
		if attempts, err := s.repository.ListAttempts(ctx); err == nil {
			for _, attempt := range attempts {
				if attempt.ID == result.AttemptID {
					record.AccountID = attempt.AccountID
					record.WorkerID = attempt.WorkerID
					break
				}
			}
		}
	}
	if s.repository != nil {
		if err := s.repository.PutOrderRecord(ctx, record); err != nil {
			return domain.OrderRecord{}, err
		}
	}
	return record, nil
}

func (s *ClusterService) ListOrderRecords() (OrderRecordList, error) {
	if s.repository == nil {
		return OrderRecordList{}, nil
	}
	records, err := s.repository.ListOrderRecords(context.Background(), maxOrderRecords)
	if err != nil {
		return OrderRecordList{}, err
	}
	return OrderRecordList{Records: records}, nil
}

func (s *ClusterService) OpenOrderPayment(recordID string) error {
	if s.repository == nil {
		return fmt.Errorf("repository is not available")
	}
	record, err := s.repository.OrderRecord(context.Background(), recordID)
	if err != nil {
		return err
	}
	s.openOrderRecordPaymentWindow(record)
	return nil
}

func (s *ClusterService) openOrderRecordPaymentWindow(record domain.OrderRecord) {
	if s.wailsApp == nil {
		log.Printf("[cluster] open order payment window skipped: wailsApp is nil")
		return
	}
	if record.PaymentURL == "" {
		log.Printf("[cluster] open order payment window skipped: payment URL is empty (orderID=%s)", record.OrderID)
		return
	}
	values := url.Values{}
	values.Set("link", record.PaymentURL)
	values.Set("title", "支付二维码")
	values.Set("project", record.ProjectName)
	values.Set("screen", record.ScreenName)
	values.Set("sku", record.SKUName)
	values.Set("buyer", strings.Join(record.BuyerNames, ", "))
	if record.PaymentExpire > 0 {
		values.Set("expire", fmt.Sprint(record.PaymentExpire))
	}
	if record.OrderTime > 0 {
		values.Set("orderTime", fmt.Sprint(record.OrderTime))
	}
	payqr.OpenWindow(s.wailsApp, "支付二维码", values)
}
