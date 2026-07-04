package biliutils

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	"fmt"
)

func (c *BiliClient) GetSafecenterCaptchaPre() (*api.SafecenterCaptchaStruct, error) {
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormData(map[string]string{
		"source": "risk",
	}).Post("https://passport.bilibili.com/x/safecenter/captcha/pre")
	if err != nil {
		return nil, fmt.Errorf("get safecenter captcha pre: %w", err)
	}
	var r api.MainApiDataRoot[*api.SafecenterCaptchaStruct]
	if err = res.Unmarshal(&r); err != nil {
		return nil, fmt.Errorf("get safecenter captcha pre: parse response: %w", err)
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	return r.Data, nil
}

func (c *BiliClient) SendSafecenterSMSCode(tmpcode, token, challenge, validate string) (string, error) {
	form := map[string]any{
		"tmp_code":        tmpcode,
		"recaptcha_token": token,
		"gee_challenge":   challenge,
		"gee_validate":    validate,
		"gee_seccode":     validate + "|jordan",
		"sms_type":        "loginTelCheck",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/safecenter/common/sms/send")
	if err != nil {
		return "", fmt.Errorf("send safecenter sms code: %w", err)
	}
	var r api.MainApiDataRoot[*api.SendSMSCodeResponseStruct]
	if err = res.Unmarshal(&r); err != nil {
		return "", fmt.Errorf("send safecenter sms code: parse response: %w", err)
	}
	if err = r.CheckValid(); err != nil {
		return "", err
	}
	return r.Data.CaptchaKey, nil
}

func (c *BiliClient) VerifySafecenterSMSCode(tmpcode, token, captchaKey, requestId, smsCode string) (*api.SafecenterLoginTelVerifyStruct, error) {
	form := map[string]any{
		"tmp_code":    tmpcode,
		"captcha_key": captchaKey,
		"code":        smsCode,
		"request_id":  requestId,
		"source":      "risk",
		"type":        "loginTelCheck",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/safecenter/login/tel/verify")
	if err != nil {
		return nil, fmt.Errorf("verify safecenter sms code: %w", err)
	}
	var r api.MainApiDataRoot[*api.SafecenterLoginTelVerifyStruct]
	if err = res.Unmarshal(&r); err != nil {
		return nil, fmt.Errorf("verify safecenter sms code: parse response: %w", err)
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	return r.Data, nil
}

func (c *BiliClient) ExhangeCookieByOauthCode(oauthCode string) (*api.ExchangeCookieResponse, error) {
	form := map[string]any{
		"code":   oauthCode,
		"source": "risk",
		"go_url": "https://www.bilibili.com",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/passport-login/web/exchange_cookie")
	if err != nil {
		return nil, fmt.Errorf("exchange cookie by oauth code: %w", err)
	}
	var r api.MainApiDataRoot[*api.ExchangeCookieResponse]
	if err = res.Unmarshal(&r); err != nil {
		return nil, fmt.Errorf("exchange cookie by oauth code: parse response: %w", err)
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	if r.Data.RefreshToken != "" {
		c.SetRefreshToken(r.Data.RefreshToken)
	}
	return r.Data, nil
}
