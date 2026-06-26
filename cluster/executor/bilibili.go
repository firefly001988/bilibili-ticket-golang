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
	client      *biliutils.BiliClient
	jar         *cookiejar.Jar
	credentials domain.Credentials
	mu          sync.Mutex
	prepared    bool
	tokenGen    token.Generator
	generatedAt time.Time
	tokens      *response.RequestTokenAndPToken
	sku         response.TicketSkuScreenID
	confirm     *api.ConfirmStruct
	buyers      []response.TicketBuyer
	submitCount uint16
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
	for _, host := range []string{"https://bilibili.com/", "https://www.bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
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
	// idBind=1: split into separate single-ticket orders, each using the same buyer.
	if b.confirm.IDBind == 1 && len(b.buyers) > 1 {
		return b.submitSplitOrders(ctx, spec)
	}
	err, code, message, order := b.client.SubmitOrder(ctx, b.tokenGen, b.generatedAt, b.tokens, strconv.FormatInt(spec.ProjectID, 10), b.sku, b.buyers, b.confirm)
	b.submitCount++
	if err != nil {
		return Outcome{Code: code, Message: message, Err: err}
	}
	if code == 100034 {
		b.sku.Price = order.PayMoney
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

// submitSplitOrders places each ticket as a separate single-ticket order.
// Used when idBind=1: one real-name buyer can purchase multiple tickets,
// but each ticket must be its own order.
func (b *BilibiliBackend) submitSplitOrders(ctx context.Context, spec domain.ExecutionSpec) Outcome {
	pid := strconv.FormatInt(spec.ProjectID, 10)
	var orderIDs []string
	for _, buyer := range b.buyers {
		single := []response.TicketBuyer{buyer}
		err, code, message, order := b.client.SubmitOrder(ctx, b.tokenGen, b.generatedAt, b.tokens, pid, b.sku, single, b.confirm)
		b.submitCount++
		if err != nil {
			// Network/transport error — return partial results if any, otherwise the error
			if len(orderIDs) > 0 {
				return Outcome{Code: 0, Message: fmt.Sprintf("split into %d orders: %s (stopped: %s)", len(orderIDs), strings.Join(orderIDs, ","), message), OrderID: strings.Join(orderIDs, ","), Err: err}
			}
			return Outcome{Code: code, Message: message, Err: err}
		}
		if code == 100034 {
			b.sku.Price = order.PayMoney
		}
		if code == 100041 || code == 100050 || code == 900002 {
			b.prepared = false
			// Token expired — return partial results if any, let Engine re-prepare and retry
			if len(orderIDs) > 0 {
				return Outcome{Code: 0, Message: fmt.Sprintf("split into %d orders: %s (token expired, retry needed)", len(orderIDs), strings.Join(orderIDs, ",")), OrderID: strings.Join(orderIDs, ",")}
			}
			return Outcome{Code: code, Message: message}
		}
		// 100003: buyer already purchased, skip to next buyer
		if code == 100003 {
			continue
		}
		if code == 0 || code == 100048 || code == 100079 {
			// Record order ID immediately, then verify status but don't block continuation
			if order.OrderId > 0 {
				orderIDs = append(orderIDs, strconv.FormatInt(order.OrderId, 10))
			}
			_, _ = b.client.GetOrderStatus(ctx, pid, order.Token, order.OrderId)
			continue
		}
		// Other non-success codes: skip to next buyer
		continue
	}
	if len(orderIDs) > 0 {
		return Outcome{Code: 0, Message: fmt.Sprintf("split into %d orders: %s", len(orderIDs), strings.Join(orderIDs, ",")), OrderID: strings.Join(orderIDs, ",")}
	}
	return Outcome{Code: -1, Message: "all buyers failed in split mode"}
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
	b.tokens, err = b.client.GetRequestTokenAndPToken(b.tokenGen, pid, b.sku, len(spec.Buyers))
	if err != nil {
		return Outcome{Err: fmt.Errorf("prepare token: %w", err)}
	}
	b.confirm, err = b.client.GetConfirmInformation(b.tokens, pid)
	if err != nil {
		return Outcome{Err: fmt.Errorf("confirm info: %w", err)}
	}
	b.buyers = make([]response.TicketBuyer, len(spec.Buyers))
	for i, buyer := range spec.Buyers {
		bt := response.Ordinary
		if project.IsForceRealName {
			bt = response.ForceRealName
		}
		b.buyers[i] = response.TicketBuyer{BuyerType: bt, ID: buyer.BuyerID, Name: buyer.Name, Tel: buyer.Tel}
	}
	b.generatedAt, b.prepared = time.Now(), true
	return Outcome{}
}
