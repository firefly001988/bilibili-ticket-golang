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
	"bilibili-ticket-golang/lib/global"
)

const maxOrderRecords = 1000

func (s *ClusterService) openOrderRecordPaymentWindowOnce(record domain.OrderRecord) {
	if record.PaymentURL == "" || record.Status == domain.SubOrderFailed || record.Status == domain.SubOrderPending {
		return
	}
	key := record.ID
	if record.OrderID != "" {
		key = record.OrderID
	}
	s.paymentWindowMu.Lock()
	if s.openedPaymentWindows[key] {
		s.paymentWindowMu.Unlock()
		return
	}
	s.openedPaymentWindows[key] = true
	s.paymentWindowMu.Unlock()
	s.openOrderRecordPaymentWindow(record)
}

func successfulSubOrderCount(subOrders []domain.SubOrderResult) int {
	count := 0
	for _, child := range subOrders {
		if child.State == domain.SubOrderSucceeded {
			count++
		}
	}
	return count
}

func (s *ClusterService) saveOrderRecords(intent domain.LogicalOrderIntent, result domain.ExecutionResult) ([]domain.OrderRecord, error) {
	if len(result.SubOrders) == 0 {
		record, err := s.saveOrderRecord(intent, result)
		if err != nil {
			return nil, err
		}
		return []domain.OrderRecord{record}, nil
	}
	records := make([]domain.OrderRecord, 0, len(result.SubOrders))
	for _, child := range result.SubOrders {
		childResult := result
		childResult.OrderID = child.OrderID
		childResult.PaymentURL = child.PaymentURL
		childResult.PaymentExpire = child.PaymentExpire
		childResult.OrderTime = child.OrderTime
		childResult.SubOrders = []domain.SubOrderResult{child}
		record, err := s.saveOrderRecord(intent, childResult)
		if err != nil {
			return records, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *ClusterService) saveOrderRecord(intent domain.LogicalOrderIntent, result domain.ExecutionResult) (domain.OrderRecord, error) {
	var child *domain.SubOrderResult
	if len(result.SubOrders) == 1 {
		child = &result.SubOrders[0]
	}
	if result.OrderID == "" && result.PaymentURL == "" && child == nil {
		return domain.OrderRecord{}, fmt.Errorf("order result has neither order id nor payment url")
	}
	recordID := result.AttemptID
	if recordID == "" {
		recordID = intent.ID
	}
	if child != nil {
		recordID += fmt.Sprintf(":sub:%d", child.BuyerIndex)
	} else if result.OrderID != "" {
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
	if child != nil {
		record.BuyerIndex = child.BuyerIndex
		record.BuyerID = child.BuyerID
		record.Status = child.State
		if child.BuyerName != "" {
			record.BuyerNames = []string{child.BuyerName}
		}
	} else {
		for _, buyer := range intent.Buyers {
			if buyer.Name != "" {
				record.BuyerNames = append(record.BuyerNames, buyer.Name)
			}
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
	// The dispatcher persists the completed attempt before invoking the
	// success handler. Read it from the repository rather than calling back
	// into the dispatcher: success handling runs while the dispatcher lock is
	// held, so taking that lock again here would deadlock the payment path.
	if s.repository != nil {
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
	record = s.hydrateOrderRecordAccount(ctx, record)
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
	ctx := context.Background()
	for i := range records {
		records[i] = s.hydrateOrderRecordAccount(ctx, records[i])
	}
	return OrderRecordList{Records: records}, nil
}

func (s *ClusterService) OpenOrderPayment(recordID string) error {
	if s.repository == nil {
		return global.NewFault("打开订单支付二维码", fmt.Errorf("repository is not available"), "请重启应用并确认集群数据库已正常初始化")
	}
	record, err := s.repository.OrderRecord(context.Background(), recordID)
	if err != nil {
		return global.NewFault("读取订单记录", err, "请刷新订单记录；如果问题持续，检查 data/employer.db 是否完整")
	}
	record = s.hydrateOrderRecordAccount(context.Background(), record)
	if orderRecordExpired(record, time.Now()) {
		return global.NewFault("打开订单支付二维码", fmt.Errorf("order payment has expired"), "该订单支付时间已过期，不能继续付款或复制支付链接")
	}
	s.openOrderRecordPaymentWindow(record)
	return nil
}

func orderRecordExpired(record domain.OrderRecord, now time.Time) bool {
	return record.PaymentExpire > 0 && !now.Before(time.Unix(record.PaymentExpire, 0))
}

func (s *ClusterService) hydrateOrderRecordAccount(ctx context.Context, record domain.OrderRecord) domain.OrderRecord {
	if s.repository == nil || record.AccountID == "" || record.AccountName != "" {
		return record
	}
	account, err := s.repository.Account(ctx, record.AccountID)
	if err != nil {
		return record
	}
	record.AccountName = account.Name
	return record
}

func orderRecordAccountText(record domain.OrderRecord) string {
	switch {
	case record.AccountName != "" && record.AccountID != "":
		return record.AccountName + " (" + record.AccountID + ")"
	case record.AccountName != "":
		return record.AccountName
	default:
		return record.AccountID
	}
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
	values.Set("account", orderRecordAccountText(record))
	if record.PaymentExpire > 0 {
		values.Set("expire", fmt.Sprint(record.PaymentExpire))
	}
	if record.OrderTime > 0 {
		values.Set("orderTime", fmt.Sprint(record.OrderTime))
	}
	payqr.OpenWindow(s.wailsApp, "支付二维码", values)
}
