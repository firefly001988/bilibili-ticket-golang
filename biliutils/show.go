package biliutils

import (
	"bilibili-ticket-golang/biliutils/token"
	"bilibili-ticket-golang/global"
	"bilibili-ticket-golang/models/bili/api"
	r "bilibili-ticket-golang/models/bili/response"
	"bilibili-ticket-golang/models/errors"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// GetProjectInformation fetches project info from the ticket mall.
// Returns project name, sale time range, hot/real-name flags, etc.
func (c *BiliClient) GetProjectInformation(projectID string) (*r.ProjectInformation, error) {
	requestURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?version=%s&id=%s&project_id=%s&requestSource=neul-next", global.FrontVersion, projectID, projectID)
	resp, err := c.client.R().Get(requestURL)
	if err != nil {
		return nil, err
	}
	var apiResp api.MainApiDataRoot[api.TicketProjectInformationStruct]
	err = resp.Unmarshal(&apiResp)
	if err != nil {
		return nil, err
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}
	idBind := apiResp.Data.IdBind != 0
	return &r.ProjectInformation{
		ProjectID:       projectID,
		StartTime:       time.Unix(apiResp.Data.Start, 0),
		EndTime:         time.Unix(apiResp.Data.End, 0),
		IsHotProject:    apiResp.Data.HotProject,
		IsNeedContact:   apiResp.Data.NeedContact,
		IsForceRealName: idBind,
		ProjectName:     apiResp.Data.Name,
	}, nil
}

// GetTicketSkuIDsByProjectID returns all ticket SKU/screen pairs for a project.
func (c *BiliClient) GetTicketSkuIDsByProjectID(projectID string) ([]r.TicketSkuScreenID, error) {
	requestURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?version=%s&id=%s&project_id=%s&requestSource=neul-next", global.FrontVersion, projectID, projectID)
	resp, err := c.client.R().Get(requestURL)
	if err != nil {
		return nil, err
	}
	var apiResp api.MainApiDataRoot[api.TicketProjectInformationStruct]
	err = resp.Unmarshal(&apiResp)
	if err != nil {
		return nil, err
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}
	tickets := make([]r.TicketSkuScreenID, 0)
	for _, screen := range apiResp.Data.ScreenList {
		for _, skuInfo := range screen.TicketList {
			ticket := r.TicketSkuScreenID{
				ScreenID: screen.ScreenId,
				SkuID:    skuInfo.SkuId,
				Name:     skuInfo.ScreenName,
				Desc:     skuInfo.Desc,
				Price:    skuInfo.Price,
				Flags: r.SaleFlagInfo{
					Number:      skuInfo.SaleFlag.Number,
					DisplayName: skuInfo.SaleFlag.DisplayName,
				},
				SaleStat: r.SaleTimeRange{
					Start: time.Unix(skuInfo.SaleStart, 0),
					End:   time.Unix(skuInfo.SaleEnd, 0),
				},
			}
			tickets = append(tickets, ticket)
		}
	}
	return tickets, nil
}

// GetRequestTokenAndPToken fetches the request token and ptoken needed for order preparation.
//
// For hot projects, a CToken (window-stats-based token) is generated and included
// in the prepare request.
func (c *BiliClient) GetRequestTokenAndPToken(tokenGen token.Generator, projectID string, ticket r.TicketSkuScreenID) (*r.RequestTokenAndPToken, error) {
	form := map[string]any{
		"project_id":    projectID,
		"screen_id":     ticket.ScreenID,
		"order_type":    1,
		"count":         1,
		"sku_id":        ticket.SkuID,
		"requestSource": "neul-next",
		"newRisk":       true,
	}
	if tokenGen.IsHotProject() {
		form["token"] = tokenGen.GenerateTokenPrepareStage()
	}
	resp, err := c.client.R().SetBodyJsonMarshal(form).Post("https://show.bilibili.com/api/ticket/order/prepare?project_id=" + projectID)
	if err != nil {
		return nil, err
	}
	var apiResp api.ShowApiDataRoot[api.RequestTokenAndPTokenStruct]
	err = resp.Unmarshal(&apiResp)
	if err != nil {
		return nil, err
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}
	return &r.RequestTokenAndPToken{
		RequestToken: apiResp.Data.Token,
		PToken:       apiResp.Data.Ptoken,
		GaiaToken:    apiResp.Data.GaData.GriskId,
	}, nil
}

// GetConfirmInformation fetches order confirmation info including buyer list,
// total price, ticket info, etc. Required before placing an order for real-name projects.
func (c *BiliClient) GetConfirmInformation(tokens *r.RequestTokenAndPToken, projectID string) (*api.ConfirmStruct, error) {
	resp, err := c.client.R().SetQueryParams(map[string]string{
		"token":         tokens.RequestToken,
		"ptoken":        tokens.PToken,
		"project_id":    projectID,
		"projectId":     projectID,
		"requestSource": "neul-next",
		"voucher":       "",
	}).Get("https://show.bilibili.com/api/ticket/order/confirmInfo")
	if err != nil {
		return nil, err
	}
	var apiResp api.ShowApiDataRoot[api.ConfirmStruct]
	err = resp.Unmarshal(&apiResp)
	if err != nil {
		return nil, err
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}
	return &apiResp.Data, nil
}

// SubmitOrder sends the final order creation request to Bilibili's ticket mall.
//
// Parameters:
//   - tokenGen: token generation strategy (CToken for hot projects, Normal otherwise)
//   - whenGenPToken: timestamp when the ptoken was generated, sent as timestamp field
//   - tokens: the RequestToken/PToken/GaiaToken from GetRequestTokenAndPToken
//   - projectID: the project ID string
//   - ticket: the target ticket SKU/screen
//   - buyer: buyer info — map[string]string for Ordinary, []map[string]any for ForceRealName
//   - buyerType: Ordinary or ForceRealName
//
// Returns: error, API response code, API message, and the order result struct.
func (c *BiliClient) SubmitOrder(tokenGen token.Generator, whenGenPToken time.Time, tokens *r.RequestTokenAndPToken, projectID string, ticket r.TicketSkuScreenID, buyer interface{}, buyerType r.BuyerType) (error, int, string, api.TicketOrderStruct) {
	form := map[string]any{
		"project_id":    projectID,
		"screen_id":     strconv.FormatInt(ticket.ScreenID, 10),
		"count":         "1",
		"pay_money":     strconv.Itoa(ticket.Price),
		"order_type":    "1",
		"timestamp":     strconv.FormatInt(whenGenPToken.Unix(), 10),
		"deviceId":      c.fingerprint.Buvidfp,
		"sku_id":        strconv.FormatInt(ticket.SkuID, 10),
		"requestSource": "neul-next",
		"token":         tokens.RequestToken,
		"newRisk":       true,
	}

	if buyerType == r.ForceRealName {
		bs, err := json.Marshal(buyer)
		if err != nil {
			return fmt.Errorf("marshal buyer info: %w", err), -1, "", api.TicketOrderStruct{}
		}
		form["buyer_info"] = string(bs)
	} else if buyerType == r.Ordinary {
		b, ok := buyer.(map[string]string)
		if !ok {
			return fmt.Errorf("invalid buyer type for Ordinary buyer: %T", buyer), -1, "", api.TicketOrderStruct{}
		}
		form["tel"] = b["tel"]
		form["buyer"] = b["name"]
	} else {
		return errors.NewTicketEmptyContactError(projectID, strconv.FormatInt(ticket.SkuID, 10), strconv.FormatInt(ticket.ScreenID, 10)), -1, "", api.TicketOrderStruct{}
	}

	if tokenGen.IsHotProject() {
		form["ctoken"] = tokenGen.GenerateTokenCreateStage(whenGenPToken)
		form["ptoken"] = tokens.PToken
		form["orderCreateUrl"] = "https://show.bilibili.com/api/ticket/order/createV2"
	}

	resp, err := c.client.R().SetBodyJsonMarshal(form).Post("https://show.bilibili.com/api/ticket/order/createV2?project_id=" + projectID)
	if err != nil {
		return err, -1, "", api.TicketOrderStruct{}
	}

	var apiResp = api.ShowApiDataRoot[api.TicketOrderStruct]{
		ErrNumber: 0,
		ErrTag:    0,
		Code:      0,
		Message:   "",
		Data: api.TicketOrderStruct{
			OrderId:         0,
			OrderCreateTime: 0,
			Token:           "",
			PayMoney:        -1,
		},
	}
	err = resp.Unmarshal(&apiResp)
	if err != nil {
		return err, -1, "", api.TicketOrderStruct{}
	}

	return nil, apiResp.GetCode(), apiResp.GetMessage(), apiResp.Data
}

// GetRealnameBuyerList fetches the list of buyers for a real-name project, which includes sensitive info like ID numbers.
// Parameters: none
//
// Returns: error and list of buyers with non-sensitive info (ID number is not included)
func (c *BiliClient) GetRealnameBuyerList() (error, []api.BuyerNoSensitiveStruct) {
	query := c.SignAppParams(map[string]any{
		"actionKey":   "appkey",
		"mobi_app":    "android",
		"build":       c.appVersion.Build,
		"mallVersion": c.appVersion.Build,
		"device":      "phone",
		"c_locale":    "zh-Hans_CN",
		"s_locale":    "zh-Hans_CN",
	})
	res, err := c.client.R().SetQueryString(query.Encode()).Get("https://show.bilibili.com/api/ticket/buyerinfo/list")
	if err != nil {
		return err, nil
	}
	var data api.ShowApiDataRoot[api.BuyerNoSensitiveInfoApiStruct]
	err = res.Unmarshal(&data)
	if err != nil {
		return err, nil
	}
	if err = data.CheckValid(); err != nil {
		return err, nil
	}
	return nil, data.Data.Vo.List
}
