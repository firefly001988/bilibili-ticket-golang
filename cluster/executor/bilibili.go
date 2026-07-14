package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/biliutils/token"
	"bilibili-ticket-golang/lib/global"
	api "bilibili-ticket-golang/lib/models/bili/api"
	response "bilibili-ticket-golang/lib/models/bili/response"
)

// BilibiliBackend adapts the existing ticket APIs into one immutable execution
// transaction. Prepared tokens are private to this attempt and never shared.
type BilibiliBackend struct {
	client       *biliutils.BiliClient
	jar          *cookiejar.Jar
	credentials  domain.Credentials
	mu           sync.Mutex
	prepared     bool
	tokenGen     token.Generator
	generatedAt  time.Time
	tokens       *response.RequestTokenAndPToken
	sku          response.TicketSkuScreenID
	confirm      *api.ConfirmStruct
	buyers       []response.TicketBuyer
	idBind       int
	subOrders    []domain.SubOrderResult
	progressSink func([]domain.SubOrderResult)
	submitCount  uint16
}

func (b *BilibiliBackend) SetProgressSink(sink func([]domain.SubOrderResult)) {
	b.progressSink = sink
}

func (b *BilibiliBackend) reportSubOrders() []domain.SubOrderResult {
	snapshot := append([]domain.SubOrderResult(nil), b.subOrders...)
	if b.progressSink != nil {
		b.progressSink(snapshot)
	}
	return snapshot
}

func (b *BilibiliBackend) failSubOrder(index, code int, message string, err error) Outcome {
	b.subOrders[index].State = domain.SubOrderFailed
	b.subOrders[index].Code = code
	b.subOrders[index].Message = message
	return Outcome{Code: code, Message: message, Err: err, SubOrders: b.reportSubOrders()}
}

func NewBilibiliBackend(credentials domain.Credentials) (*BilibiliBackend, error) {
	return NewBilibiliBackendWithSolver(credentials, nil)
}

func NewBilibiliBackendWithSolver(credentials domain.Credentials, solver biliutils.CaptchaSolverFn) (*BilibiliBackend, error) {
	jar := cookiejar.New(nil)
	for _, saved := range credentials.CookieJar {
		host := strings.TrimPrefix(saved.Domain, ".")
		if host == "" {
			host = "www.bilibili.com"
		}
		u, _ := url.Parse("https://" + host + "/")
		cookie := &http.Cookie{Name: saved.Name, Value: saved.Value, Domain: saved.Domain, Path: saved.Path, Secure: saved.Secure, HttpOnly: saved.HTTPOnly}
		if saved.Expires > 0 {
			cookie.Expires = time.Unix(saved.Expires, 0)
		}
		jar.SetCookies(u, []*http.Cookie{cookie})
	}
	for _, host := range []string{"https://api.bilibili.com/", "https://bilibili.com/", "https://www.bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
		cookies := make([]*http.Cookie, 0, len(credentials.Cookies))
		for name, value := range credentials.Cookies {
			cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
		}
		if u, err := http.NewRequest(http.MethodGet, host, nil); err == nil {
			jar.SetCookies(u.URL, cookies)
		}
	}
	var client *biliutils.BiliClient
	var err error
	if len(credentials.DeviceProfile) > 0 {
		var profile biliutils.DeviceProfile
		if decodeErr := json.Unmarshal(credentials.DeviceProfile, &profile); decodeErr != nil {
			return nil, fmt.Errorf("decode device profile: %w", decodeErr)
		}
		client, err = biliutils.NewBiliClientWithDeviceProfile(jar, profile)
	} else {
		client, err = biliutils.NewBiliClientWithCookiejar(jar)
	}
	if err != nil {
		return nil, err
	}
	// Workers MUST NOT call CheckAndUpdateCookie().  Credential rotation is
	// an employer-side concern — doing it on the worker would cause race
	// conditions when multiple workers share the same account.
	client.SetRefreshToken(credentials.RefreshToken)
	if solver != nil {
		client.SetCaptchaSolver(solver)
	}
	if len(credentials.DeviceProfile) == 0 {
		credentials.DeviceProfile, _ = json.Marshal(client.ExportDeviceProfile())
	}
	return &BilibiliBackend{client: client, jar: jar, credentials: credentials}, nil
}

func (b *BilibiliBackend) Credentials() domain.Credentials {
	b.mu.Lock()
	defer b.mu.Unlock()
	updated := make(map[string]string)
	full := make([]domain.HTTPCookie, 0)
	for _, entry := range b.jar.AllEntries() {
		updated[entry.Name] = entry.Value
		full = append(full, domain.HTTPCookie{Name: entry.Name, Value: entry.Value, Domain: entry.Domain, Path: entry.Path, Secure: entry.Secure, HTTPOnly: entry.HttpOnly, Expires: entry.Expires})
	}
	if len(updated) > 0 {
		b.credentials.Cookies = updated
	}
	b.credentials.CookieJar = full
	b.credentials.RefreshToken = b.client.GetRefreshToken()
	result := b.credentials
	result.Version++
	return result
}

// ClientAndJar returns the underlying BiliClient and cookie jar for
// direct API calls that are not part of the normal execution flow
// (e.g. buyer management RPCs on the worker).
func (b *BilibiliBackend) ClientAndJar() (*biliutils.BiliClient, *cookiejar.Jar) {
	return b.client, b.jar
}

func (b *BilibiliBackend) Attempt(ctx context.Context, spec domain.ExecutionSpec) Outcome {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Outcome{Err: err}
	}
	if b.submitCount >= global.MaxTokenRefreshCount {
		b.prepared = false
		b.submitCount = 0
	}
	if !b.prepared {
		if out := b.prepare(spec); out.Err != nil || out.Code != 0 {
			return out
		}
	}
	// Reflow stock check: skip submit if stock is unavailable.
	if spec.ReflowStockCheck {
		stockErr, stock := b.client.StockCheck(ctx, spec.ProjectID, spec.ScreenID, b.sku.SkuID)
		if stockErr != nil {
			return Outcome{Code: -1, Message: "stock check failed", Err: stockErr}
		}
		// StockStatus: 1 = temporary sold out, 2 = sold out, 3 = has stock
		if !stock.HasStock && stock.StockStatus != 3 {
			switch stock.StockStatus {
			case 1:
				return Outcome{Code: -1, Message: "temporary sold out"}
			case 2:
				return Outcome{Code: -1, Message: "sold out"}
			default:
				return Outcome{Code: -1, Message: "no stock available"}
			}
		}
	}
	// idBind=1 orders are independent transactions. Each ticket receives its
	// own prepare/confirm tokens before createV2.
	if b.idBind == 1 && len(b.buyers) > 1 {
		return b.submitSplitOrders(ctx, spec)
	}
	err, code, message, order := b.client.SubmitOrder(ctx, b.tokenGen, b.generatedAt, b.tokens, strconv.FormatInt(spec.ProjectID, 10), b.sku, b.buyers, b.confirm)
	b.submitCount++
	if err != nil {
		return Outcome{Code: code, Message: message, Err: err}
	}
	if code == 100034 && order.PayMoney > 0 {
		b.confirm.PayMoney = order.PayMoney
	}
	if code == 100041 || code == 100050 || code == 900002 {
		b.prepared = false
	}
	if code == 0 || code == 100048 || code == 100079 {
		statusErr, orderStatus := b.client.GetOrderStatus(ctx, strconv.FormatInt(spec.ProjectID, 10), order.Token, order.OrderId)
		if statusErr != nil || orderStatus == nil || order.OrderId != responseOrderID(orderStatus) {
			return Outcome{Code: code, Message: "order confirmation pending", Err: statusErr}
		}
		out := Outcome{Code: code, Message: message, OrderID: strconv.FormatInt(order.OrderId, 10)}
		applyPaymentStatus(&out, order.Token, order.OrderId, order.OrderCreateTime, orderStatus)
		return out
	}
	return Outcome{Code: code, Message: message}
}

// submitSplitOrders places each ticket as a separate, fully prepared order.
// Completed orders are retained across Engine retries so a transient failure
// cannot submit the same buyer twice.
func (b *BilibiliBackend) submitSplitOrders(ctx context.Context, spec domain.ExecutionSpec) Outcome {
	pid := strconv.FormatInt(spec.ProjectID, 10)
	if len(b.subOrders) != len(b.buyers) {
		b.subOrders = make([]domain.SubOrderResult, len(b.buyers))
		for index, buyer := range b.buyers {
			b.subOrders[index] = domain.SubOrderResult{BuyerIndex: index, BuyerID: buyer.ID, BuyerName: buyer.Name, State: domain.SubOrderPending}
		}
		b.reportSubOrders()
	}

	for index, buyer := range b.buyers {
		if b.subOrders[index].State == domain.SubOrderSucceeded {
			continue
		}
		b.subOrders[index].State = domain.SubOrderPending
		b.subOrders[index].Code = 0
		b.subOrders[index].Message = ""
		b.reportSubOrders()
		if err := ctx.Err(); err != nil {
			return b.failSubOrder(index, -1, err.Error(), err)
		}

		// Prepare count and create count must describe the same transaction.
		tokens, err := b.client.GetRequestTokenAndPToken(b.tokenGen, pid, b.sku, 1)
		if err != nil {
			wrapped := fmt.Errorf("prepare split order %d: %w", index+1, err)
			return b.failSubOrder(index, -1, wrapped.Error(), wrapped)
		}
		confirm, err := b.client.GetConfirmInformation(tokens, pid)
		if err != nil {
			wrapped := fmt.Errorf("confirm split order %d: %w", index+1, err)
			return b.failSubOrder(index, -1, wrapped.Error(), wrapped)
		}
		generatedAt := time.Now()
		single := []response.TicketBuyer{buyer}

		var code int
		var message string
		var order api.TicketOrderStruct
		for priceRetry := 0; priceRetry < 2; priceRetry++ {
			err, code, message, order = b.client.SubmitOrder(ctx, b.tokenGen, generatedAt, tokens, pid, b.sku, single, confirm)
			b.submitCount++
			if err != nil {
				if code == 429 {
					return b.failSubOrder(index, code, message, nil)
				}
				return b.failSubOrder(index, code, message, err)
			}
			if code != 100034 || order.PayMoney <= 0 {
				break
			}
			// PayMoney is already the complete order total.
			confirm.PayMoney = order.PayMoney
		}

		if code == 100041 || code == 100050 || code == 900002 {
			return b.failSubOrder(index, code, message, nil)
		}
		if code != 0 && code != 100048 && code != 100079 {
			return b.failSubOrder(index, code, message, nil)
		}
		if order.OrderId <= 0 {
			return b.failSubOrder(index, code, "split order returned no order id", nil)
		}
		statusErr, status := b.client.GetOrderStatus(ctx, pid, order.Token, order.OrderId)
		childOutcome := Outcome{}
		applyPaymentStatus(&childOutcome, order.Token, order.OrderId, order.OrderCreateTime, status)
		b.subOrders[index] = domain.SubOrderResult{
			BuyerIndex: index, BuyerID: buyer.ID, BuyerName: buyer.Name,
			State: domain.SubOrderSucceeded, OrderID: strconv.FormatInt(order.OrderId, 10),
			PaymentURL: childOutcome.PaymentURL, PaymentExpire: childOutcome.PaymentExpire,
			OrderTime: childOutcome.OrderTime, Code: code, Message: message,
		}
		b.reportSubOrders()
		if statusErr != nil {
			// The create response already contains a valid order ID. Preserve the
			// successful child and allow the remaining children to continue.
			continue
		}
	}
	ids := make([]string, 0, len(b.subOrders))
	for _, child := range b.subOrders {
		if child.State != domain.SubOrderSucceeded {
			return Outcome{Code: -1, Message: "split orders incomplete", SubOrders: b.reportSubOrders()}
		}
		ids = append(ids, child.OrderID)
	}
	joined := strings.Join(ids, ",")
	return Outcome{Code: 0, Message: fmt.Sprintf("split into %d orders: %s", len(ids), joined), OrderID: joined, SubOrders: b.reportSubOrders()}
}

func responseOrderID(status *api.OrderStatusStruct) int64 {
	if status == nil {
		return 0
	}
	value, _ := strconv.ParseInt(status.OrderId, 10, 64)
	return value
}

func applyPaymentStatus(out *Outcome, token string, orderID, orderCreateTime int64, status *api.OrderStatusStruct) {
	if out == nil {
		return
	}
	if status != nil && status.PayParam.CodeUrl != "" {
		out.PaymentURL = status.PayParam.CodeUrl
	} else if orderID > 0 && token != "" {
		out.PaymentURL = fmt.Sprintf("https://show.bilibili.com/orderdetail?id=%d&token=%s", orderID, url.QueryEscape(token))
	}
	if status != nil {
		if status.PayParam.OrderCreateTime != "" {
			out.OrderTime = parseOrderTimestamp(status.PayParam.OrderCreateTime)
		}
		if status.PayParam.OrderExpire != "" {
			if sec, err := strconv.ParseInt(status.PayParam.OrderExpire, 10, 64); err == nil && sec > 0 {
				switch {
				case sec > 1e12:
					out.PaymentExpire = sec / 1000
				case sec > 1e9:
					out.PaymentExpire = sec
				case out.OrderTime > 0:
					out.PaymentExpire = out.OrderTime + sec
				default:
					out.PaymentExpire = time.Now().Unix() + sec
				}
			}
		}
	}
	if out.OrderTime == 0 && orderCreateTime > 0 {
		out.OrderTime = normalizeOrderTimestamp(orderCreateTime)
	}
	if out.PaymentExpire == 0 && out.OrderTime > 0 {
		out.PaymentExpire = out.OrderTime + 900
	}
}

func parseOrderTimestamp(value string) int64 {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return normalizeOrderTimestamp(n)
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local); err == nil {
		return t.Unix()
	}
	return 0
}

func normalizeOrderTimestamp(value int64) int64 {
	if value > 1e12 {
		return value / 1000
	}
	return value
}

func (b *BilibiliBackend) prepare(spec domain.ExecutionSpec) Outcome {
	pid := strconv.FormatInt(spec.ProjectID, 10)
	project, err := b.client.GetProjectInformationNew(pid)
	if err != nil {
		return Outcome{Err: fmt.Errorf("project info: %w", err)}
	}
	if project.IsHotProject {
		ec := token.NewEncodeData(b.client.GetBrowserUA(), fmt.Sprintf("https://mall.bilibili.com/neul-next/ticket-renovation/detail.html?id=%d", spec.ProjectID))
		b.tokenGen = token.NewCToken2026Generator(ec)
	} else {
		b.tokenGen = token.NewNormalTokenGenerator()
	}
	all, err := b.client.GetTicketSkuIDsByProjectIDNew(pid)
	if err != nil {
		return Outcome{Err: fmt.Errorf("sku list: %w", err)}
	}
	found := false
	for _, sku := range all {
		if sku.SkuID == spec.SKUID && sku.ScreenID == spec.ScreenID {
			b.sku, found = sku, true
			break
		}
	}
	if !found {
		return Outcome{Code: 100016, Message: "sku not found"}
	}
	b.idBind = project.IDBind
	b.buyers = make([]response.TicketBuyer, len(spec.Buyers))
	for i, buyer := range spec.Buyers {
		bt := response.Ordinary
		if project.IsForceRealName {
			bt = response.ForceRealName
		}
		b.buyers[i] = response.TicketBuyer{BuyerType: bt, ID: buyer.BuyerID, Name: buyer.Name, Tel: buyer.Tel}
	}
	// Split mode prepares each one-ticket transaction immediately before its
	// corresponding create request.
	if b.idBind == 1 && len(b.buyers) > 1 {
		b.prepared = true
		return Outcome{}
	}
	b.tokens, err = b.client.GetRequestTokenAndPToken(b.tokenGen, pid, b.sku, len(spec.Buyers))
	if err != nil {
		return Outcome{Err: fmt.Errorf("prepare token: %w", err)}
	}
	b.confirm, err = b.client.GetConfirmInformation(b.tokens, pid)
	if err != nil {
		return Outcome{Err: fmt.Errorf("confirm info: %w", err)}
	}
	// Prefer the transaction's own id_bind when it differs from project
	// metadata. A multi-ticket prepare is discarded before entering split mode.
	if b.confirm.IDBind != 0 {
		b.idBind = b.confirm.IDBind
	}
	if b.idBind == 1 && len(b.buyers) > 1 {
		b.tokens = nil
		b.confirm = nil
		b.prepared = true
		return Outcome{}
	}
	b.generatedAt, b.prepared = time.Now(), true
	return Outcome{}
}
