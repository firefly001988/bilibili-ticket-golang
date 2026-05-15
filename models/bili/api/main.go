package api

import (
	"bilibili-ticket-golang/models/errors"
)

// MainApiDataRoot 主站API基类
type MainApiDataRoot[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func (r *MainApiDataRoot[T]) CheckValid() error {
	if r.Code != 0 {
		return errors.NewBilibiliAPIError(r.Code, r.Message)
	}
	return nil
}

type QRLoginKeyStruct struct {
	URL       string `json:"url"`
	QRCodeKey string `json:"qrcode_key"`
}

type VerifyQRLoginStateStruct struct {
	RefreshToken string `json:"refresh_token"`
	Timestamp    int64  `json:"timestamp"`
	Code         int    `json:"code"`
	Message      string `json:"message"`
}

type GetLoginInfoStruct struct {
	Login bool   `json:"isLogin"`
	Name  string `json:"uname,omitempty"`
	UID   int64  `json:"mid,omitempty"`
	Face  string `json:"face,omitempty"`
}

type GetBVUID34Struct struct {
	BVUID3 string `json:"b_3"`
	BVUID4 string `json:"b_4"`
}

type NeedRefreshStruct struct {
	NeedRefresh bool  `json:"refresh"`
	Timestamp   int64 `json:"timestamp"`
}

type RefreshTokenStruct struct {
	RefreshToken string `json:"refresh_token"`
}

type BiliTicketStruct struct {
	Ticket  string `json:"ticket"`
	Created int64  `json:"create_at"`
	TTL     int    `json:"ttl"`
}

type BiliAppVersionStruct struct {
	Version string `json:"version"`
	Build   int64  `json:"build"`
}

type WbiStruct struct {
	WbiImg struct {
		ImgUrl string `json:"img_url"`
		SubUrl string `json:"sub_url"`
	} `json:"wbi_img"`
}

type TicketProjectInformationStruct struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Start       int64  `json:"start_time"`
	End         int64  `json:"end_time"`
	HotProject  bool   `json:"hotProject"`
	NeedContact bool   `json:"need_contact"`
	IdBind      int    `json:"id_bind"` // I think it's similar to the `NeedContact`. 1 is force Real Name Authentication. 0 is not.
	ScreenList  []struct {
		SaleFlag struct {
			Number      int    `json:"number"`
			DisplayName string `json:"display_name"`
		} `json:"saleFlag"`
		ScreenId     int64  `json:"id"`
		StartTime    int64  `json:"start_time"`
		Name         string `json:"name"`
		Type         int    `json:"type"`
		TicketType   int    `json:"ticket_type"`
		ScreenType   int    `json:"screen_type"`
		DeliveryType int    `json:"delivery_type"`
		PickSeat     int    `json:"pick_seat"`
		TicketList   []struct {
			Price     int    `json:"price"`
			Desc      string `json:"desc"`
			SaleStart int64  `json:"saleStart"`
			SaleEnd   int64  `json:"saleEnd"`
			IsSale    int    `json:"is_sale"`
			SkuId     int64  `json:"id"`
			SaleFlag  struct {
				Number      int    `json:"number"`
				DisplayName string `json:"display_name"`
			} `json:"sale_flag"`
			ScreenName string `json:"screen_name"`
		} `json:"ticket_list"`
	} `json:"screen_list"`
}

type VoucherStruct struct {
	Voucher string `json:"v_voucher"`
}
