package api

import (
	"bilibili-ticket-golang/models/errors"
)

// ShowApiDataRoot 漫展API基类
type ShowApiDataRoot[T any] struct {
	ErrTag    int    `json:"errtag"`
	ErrNumber int    `json:"errno"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Msg       string `json:"msg"`
	Data      T      `json:"data"`
}

func (r *ShowApiDataRoot[T]) GetCode() int {
	if r.ErrNumber != 0 {
		return r.ErrNumber
	} else {
		return r.Code
	}
}

func (r *ShowApiDataRoot[T]) GetMessage() string {
	if r.ErrNumber != 0 {
		return r.Msg
	} else {
		return r.Message
	}
}

func (r *ShowApiDataRoot[T]) CheckValid() error {
	if r.ErrNumber != 0 || r.Code != 0 {
		return errors.NewBilibiliAPIError(r.GetCode(), r.GetMessage())
	}
	return nil
}

type RequestTokenAndPTokenStruct struct {
	Token  string `json:"token"`
	Shield struct {
		Open int `json:"open"`
	} `json:"shield"`
	ProjectName interface{} `json:"project_name"`
	ScreenName  interface{} `json:"screen_name"`
	ProjectImg  interface{} `json:"project_img"`
	GaData      struct {
		RiskLevel  int           `json:"risk_level"`
		GriskId    string        `json:"grisk_id"`
		Decisions  []interface{} `json:"decisions"`
		RiskParams interface{}   `json:"riskParams"`
		RiskResult int           `json:"riskResult"`
		Open       interface{}   `json:"open"`
	} `json:"ga_data"`
	SuccessSeats interface{}   `json:"success_seats"`
	FailedSeats  []interface{} `json:"failed_seats"`
	Ptoken       string        `json:"ptoken"`
}

// BuyerListInfo contains the buyer list and max purchase limit for order confirmation.
type BuyerListInfo struct {
	List     []BuyerStruct `json:"list"`
	MaxLimit int           `json:"max_limit"`
}

// TicketInfoDetail contains the ticket price, count, and SKU info for order confirmation.
type TicketInfoDetail struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
	Count int    `json:"count"`
	SkuId int    `json:"sku_id"`
}

type ConfirmStruct struct {
	Count          int              `json:"count"`
	BuyerList      BuyerListInfo    `json:"buyerList"`
	HotProject     bool             `json:"hotProject"`
	OrderCreateUrl string           `json:"orderCreateUrl"`
	ProjectId      int              `json:"project_id"`
	ProjectName    string           `json:"project_name"`
	ScreenId       int              `json:"screen_id"`
	ScreenName     string           `json:"screen_name"`
	BuyerInfo      string           `json:"buyer_info"`
	ItemTotalMoney int              `json:"item_total_money"`
	PayMoney       int              `json:"pay_money"`
	TicketInfo     TicketInfoDetail `json:"ticket_info"`
	IDBind         int              `json:"id_bind"`
	IsPackage      int              `json:"is_package"`
	NeedContact    int              `json:"need_contact"`
}

type BuyerStruct struct {
	Id                  int64       `json:"id"`
	Uid                 int64       `json:"uid"`
	AccountId           int64       `json:"accountId"`
	Name                string      `json:"name"`
	Buyer               interface{} `json:"buyer"`
	Tel                 string      `json:"tel"`
	DisabledErr         interface{} `json:"disabledErr"`
	AccountChannel      string      `json:"account_channel"`
	PersonalId          string      `json:"personal_id"`
	IdCardFront         string      `json:"id_card_front"`
	IdCardBack          string      `json:"id_card_back"`
	IsDefault           int         `json:"is_default"`
	IdType              int         `json:"id_type"`
	VerifyStatus        int         `json:"verify_status"`
	IsBuyerInfoVerified bool        `json:"isBuyerInfoVerified"`
	IsBuyerValid        bool        `json:"isBuyerValid"`
}

type TicketOrderStruct struct {
	OrderId         int64  `json:"orderId"`
	OrderCreateTime int64  `json:"orderCreateTime"`
	Token           string `json:"token"`
	PayMoney        int    `json:"pay_money"`
}

type BuyerNoSensitiveInfoApiStruct struct {
	Vo struct {
		List []BuyerNoSensitiveStruct `json:"list"`
	} `json:"vo"`
}

type BuyerNoSensitiveStruct struct {
	Id           int64  `json:"id"`
	Uid          int64  `json:"uid"`
	Name         string `json:"name"`
	IdType       int    `json:"idType"`
	IdName       string `json:"idName"`
	IdCard       string `json:"idCard"`
	Tel          string `json:"tel"`
	ViewType     string `json:"viewType"`
	VerifyStatus int    `json:"verifyStatus"`
	Status       int    `json:"status"`
}

// BuyerNoSensitiveInfoNewApiStruct is the response data from the new
// buyerinfo/list API, which returns the list directly (not wrapped in vo)
// along with max_limit and isDynamic fields.
type BuyerNoSensitiveInfoNewApiStruct struct {
	MaxLimit  int                         `json:"max_limit"`
	IsDynamic int                         `json:"isDynamic"`
	List      []BuyerNoSensitiveNewStruct `json:"list"`
}

// BuyerNoSensitiveNewStruct extends BuyerNoSensitiveStruct with additional
// fields returned by the new buyerinfo/list API (def, cardImgFront,
// cardImgBack, defaultImgUrl, editImgUrl).
type BuyerNoSensitiveNewStruct struct {
	Id            int64  `json:"id"`
	Uid           int64  `json:"uid"`
	Name          string `json:"name"`
	PersonalId    string `json:"personal_id"`
	IdType        int    `json:"idType"`
	Tel           string `json:"tel"`
	ViewType      string `json:"viewType"`
	DefaultImgUrl string `json:"defaultImgUrl"`
	VerifyStatus  int    `json:"verifyStatus"`
}

type OrderStatusStruct struct {
	OrderId string `json:"order_id"`
}
