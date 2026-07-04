package api

import (
	"bilibili-ticket-golang/lib/models/errors"
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
	IsVip int    `json:"vipStatus,omitempty"` // 0 = not VIP, 1 = VIP
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
	IdBind      int    `json:"id_bind"` // 0 = 无实名, 1 = 单人实名可买多张票, 2 = 一票一实名
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

// TicketProjectInformationNewStruct is the response data from mall.bilibili.com/mall-search-items/items_detail/info.
// Unlike the getV2 API, this endpoint uses a floor-based layout and different field names.
type TicketProjectInformationNewStruct struct {
	ProjectId     int    `json:"projectId"`
	ProjectName   string `json:"projectName"`
	EndTime       int64  `json:"endTime"`
	CurrentTime   int64  `json:"currentTime"`
	HotProject    bool   `json:"hotProject"`
	IdBind        int    `json:"idBind"` // 0 = 无实名, 1 = 单人实名可买多张票, 2 = 一票一实名
	ContactNotice int    `json:"contactNotice"`
	BuyerInfo     string `json:"buyerInfo"` // "idBind,needContact" e.g. "2,1"
	ScreenList    []struct {
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
			ScreenName  string `json:"screen_name"`
			StaticLimit struct {
				BuyLimit     int `json:"num"`
				BuyLimitType int `json:"num_type"`
			} `json:"static_limit"`
		} `json:"ticket_list"`
	} `json:"screenList"`
}

type VoucherStruct struct {
	Voucher string `json:"v_voucher"`
}

type CountryListStruct struct {
	Common []CountryCidStruct `json:"common"`
	Others []CountryCidStruct `json:"others"`
}

type CountryCidStruct struct {
	Id        int    `json:"id"`
	CName     string `json:"cname"`
	CountryId string `json:"country_id"`
}

type LoginCaptchaStruct struct {
	Type    string `json:"type"`
	Geetest struct {
		Gt        string `json:"gt"`
		Challenge string `json:"challenge"`
	} `json:"geetest"`
	Token string `json:"token"`
}

type SendSMSCodeResponseStruct struct {
	CaptchaKey string `json:"captcha_key"`
}

// PasswordKeyStruct 密码登录第一步：获取公钥和盐。
// 对应 POST /x/passport-login/web/key
type PasswordKeyStruct struct {
	Hash string `json:"hash"` // 密码盐值，16字符，有效20秒
	Key  string `json:"key"`  // RSA 公钥，PEM 格式
}

// PasswordLoginResponseStruct 密码登录返回。
// 对应 POST /x/passport-login/web/login
type PasswordLoginResponseStruct struct {
	Message      string `json:"message"`
	RefreshToken string `json:"refresh_token"`
	Status       int    `json:"status"`
	Timestamp    int64  `json:"timestamp"`
	URL          string `json:"url"`          // URL 可能是bili游戏URL, 可能是验证URL
	IsNew        bool   `json:"is_new"`       // 风险验证时存在
	Hint         string `json:"hint"`         // 风险验证时存在
	InRegAudit   int    `json:"in_reg_audit"` // 风险验证时存在
}

type VerifySMSCodeResponseStruct struct {
	RefreshToken string `json:"refresh_token"`
	IsNew        bool   `json:"is_new"`
	Hint         string `json:"hint"`
	Status       int    `json:"status"`
	Message      string `json:"message"`
	URL          string `json:"url"`
}

type SafecenterCaptchaStruct struct {
	Challenge string `json:"gee_challenge,omitempty"`
	Gt        string `json:"gee_gt,omitempty"`
	Token     string `json:"recaptcha_token"`
	Type      string `json:"recaptcha_type"`
}

type SafecenterLoginTelVerifyStruct struct {
	OauthCode string `json:"code"`
}

type ExchangeCookieResponse struct {
	RefreshToken string `json:"refresh_token"`
}
