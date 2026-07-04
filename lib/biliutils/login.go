package biliutils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	"bilibili-ticket-golang/lib/models/bili/api"
)

// GetQRCodeUrlAndKey fetches a QR code URL and key for Bilibili login.
// Returns the QR code image URL and the poll key for status checking.
func (c *BiliClient) GetQRCodeUrlAndKey() (*api.QRLoginKeyStruct, error) {
	res, err := c.client.R().Get("https://passport.bilibili.com/x/passport-login/web/qrcode/generate?source=main-fe-header")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.QRLoginKeyStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	return r.Data, nil
}

// GetQRLoginState polls the QR code login status.
// qrcodeKey is the key returned by GetQRCodeUrlAndKey.
// Returns Code=0 and RefreshToken when scan is confirmed, other codes otherwise.
func (c *BiliClient) GetQRLoginState(qrcodeKey string) (*api.VerifyQRLoginStateStruct, error) {
	res, err := c.client.R().SetQueryParam("qrcode_key", qrcodeKey).Get("https://passport.bilibili.com/x/passport-login/web/qrcode/poll")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.VerifyQRLoginStateStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	c.SetRefreshToken(r.Data.RefreshToken)
	return r.Data, nil
}

func (c *BiliClient) GetLoginCaptcha() (*api.LoginCaptchaStruct, error) {
	res, err := c.client.R().Get("https://passport.bilibili.com/x/passport-login/captcha?source=main_web")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.LoginCaptchaStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	return r.Data, nil
}

// GetCountryList fetches the list of countries from Bilibili's API.
// Returns a CountryListStruct containing country codes and names.
func (c *BiliClient) GetCountryList() (*api.CountryListStruct, error) {
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).Get("https://passport.bilibili.com/web/generic/country/list")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.CountryListStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	return r.Data, nil
}

// SendSMSCode sends an SMS verification code to the specified phone number.
// phone is the phone number, cid is the country code (e.g., "86" for China).
// Returns nil if the request is successful, otherwise an error.
// Cooldown: 60 seconds per phone number. If the same phone number is requested again within 60 seconds, the API will return an error.
// Validation time: 5 minutes.
func (c *BiliClient) SendSMSCode(phone, cid int64, captchaToken string, captchaValidate string, captchaChallenge string) (string, error) {
	form := map[string]any{
		"tel":       phone,
		"cid":       cid,
		"source":    "main_web",
		"token":     captchaToken,
		"challenge": captchaChallenge,
		"validate":  captchaValidate,
		"seccode":   captchaValidate + "|jordan",
		"go_url":    "https://www.bilibili.com",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/passport-login/web/sms/send")
	if err != nil {
		return "", err
	}
	var r api.MainApiDataRoot[*api.SendSMSCodeResponseStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return "", err
	}
	if err = r.CheckValid(); err != nil {
		return "", err
	}
	return r.Data.CaptchaKey, nil
}

// VerifySMSCode verifies the SMS code sent to the phone number.
// phone is the phone number, captchaKey is the captcha key returned by SendSMSCode.
func (c *BiliClient) VerifySMSCode(phone, cid int64, captchaKey string, smsCode string) (*api.VerifySMSCodeResponseStruct, error) {
	form := map[string]any{
		"tel":         phone,
		"cid":         cid,
		"code":        smsCode,
		"source":      "main_web",
		"captcha_key": captchaKey,
		"go_url":      "https://www.bilibili.com",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/passport-login/web/login/sms")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.VerifySMSCodeResponseStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	if err = r.CheckValid(); err != nil {
		return nil, err
	}
	if r.Data.RefreshToken != "" {
		c.SetRefreshToken(r.Data.RefreshToken)
	}
	return r.Data, nil
}

// =============================================================================
// Password Login
// =============================================================================

// GetPasswordKey fetches the RSA public key and salt for password login.
// salt is a 16-char hex string, valid for 20 seconds.
// pubKey is the RSA public key in PEM format.
func (c *BiliClient) GetPasswordKey() (salt string, pubKeyPEM string, err error) {
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
			"_":                  fmt.Sprintf("%d", c.Now().UnixMilli()),
		},
	).Get("https://passport.bilibili.com/x/passport-login/web/key")
	if err != nil {
		return "", "", err
	}
	var r api.MainApiDataRoot[api.PasswordKeyStruct]
	if err = res.Unmarshal(&r); err != nil {
		return "", "", err
	}
	if err = r.CheckValid(); err != nil {
		return "", "", err
	}
	return r.Data.Hash, r.Data.Key, nil
}

// encryptPassword encrypts the password using the RSA public key from Bilibili.
// Steps: salt + password → RSA-encrypt with pubKey → base64 encode.
func encryptPassword(password, salt, pubKeyPEM string) (string, error) {
	block, _ := pem.Decode([]byte(pubKeyPEM))
	if block == nil {
		return "", fmt.Errorf("password login: PEM decode failed")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("password login: parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("password login: key is not RSA")
	}

	// salt concatenated before plaintext password, then encrypted together
	plaintext := salt + password
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("password login: RSA encrypt: %w", err)
	}

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// EncryptPassword encrypts a plaintext password using the salt and public key
// returned by GetPasswordKey.
func EncryptPassword(password, salt, pubKeyPEM string) (string, error) {
	return encryptPassword(password, salt, pubKeyPEM)
}

// PasswordLogin performs Bilibili web password login.
//
// The caller must obtain and solve the captcha separately, then pass
// token, challenge, validate and seccode (validate + "|jordan").
//
// On success, RefreshToken and cookies are saved automatically.
// If status is not 0, the login failed.
// If status is 2, need sms code verification.
func (c *BiliClient) PasswordLogin(username, encryptedPassword, token, challenge, validate, seccode string) (*api.PasswordLoginResponseStruct, error) {
	form := map[string]any{
		"username":  username,
		"password":  encryptedPassword,
		"token":     token,
		"challenge": challenge,
		"validate":  validate,
		"seccode":   seccode,
		"source":    "main_web",
		"go_url":    "https://www.bilibili.com",
	}
	res, err := c.client.R().SetQueryParams(
		map[string]string{
			"x-bili-redirect":    "1",
			"x-bili-locale-json": `{"c_locale":{"language":"zh","region":"CN"},"always_translate":true}`,
		},
	).SetFormDataAnyType(form).Post("https://passport.bilibili.com/x/passport-login/web/login")
	if err != nil {
		return nil, fmt.Errorf("password login: %w", err)
	}

	var loginResp api.MainApiDataRoot[*api.PasswordLoginResponseStruct]
	if err = res.Unmarshal(&loginResp); err != nil {
		return nil, fmt.Errorf("password login: parse response: %w", err)
	}
	if err = loginResp.CheckValid(); err != nil {
		return nil, err
	}

	if loginResp.Data.RefreshToken != "" {
		c.SetRefreshToken(loginResp.Data.RefreshToken)
	}

	return loginResp.Data, nil
}
