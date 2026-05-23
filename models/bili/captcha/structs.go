package captcha

type RegisterVoucherResponse struct {
	Type    string `json:"type"`  // e.g. "click", "slide"
	Token   string `json:"token"` // the voucher token (v_voucher)
	Geetest struct {
		Gt        string `json:"gt"`        // geetest gt
		Challenge string `json:"challenge"` // geetest challenge
	} `json:"geetest"` // geetest parameters for solving the captcha
}

type ValidateResponse struct {
	Valid   int    `json:"is_valid"` // whether the captcha was solved correctly
	GriskID string `json:"grisk_id"` // risk ID associated with this captcha attempt
}
