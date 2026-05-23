package biliutils

import (
	"bilibili-ticket-golang/models/bili/api"
	"bilibili-ticket-golang/models/bili/captcha"
	"fmt"
)

func (c *BiliClient) registerVoucher(voucher string) (error, string, string, string) {
	res, err := c.client.R().SetFormData(map[string]string{
		"csrf":      c.getCSRFFromCookie(),
		"v_voucher": voucher,
	}).Post("https://api.bilibili.com/x/gaia-vgate/v1/register")
	if err != nil {
		return err, "", "", ""
	}
	var r api.MainApiDataRoot[captcha.RegisterVoucherResponse]
	err = res.Unmarshal(&r)
	if err != nil {
		return err, "", "", ""
	}
	if err = r.CheckValid(); err != nil {
		return err, "", "", ""
	}
	if r.Data.Type != "geetest" {
		return fmt.Errorf("unexpected voucher type: %s", r.Data.Type), "", "", ""
	}
	return nil, r.Data.Token, r.Data.Geetest.Gt, r.Data.Geetest.Challenge
}

func (c *BiliClient) vaildate(token, challenge, validate, gt string) (error, string) {
	res, err := c.client.R().SetFormData(map[string]string{
		"csrf":      c.getCSRFFromCookie(),
		"challenge": challenge,
		"validate":  validate,
		"gt":        gt,
		"seccode":   validate + "|jordan",
	}).Post("https://api.bilibili.com/x/gaia-vgate/v1/register")
	if err != nil {
		return err, ""
	}
	var r api.MainApiDataRoot[captcha.ValidateResponse]
	err = res.Unmarshal(&r)
	if err != nil {
		return err, ""
	}
	if err = r.CheckValid(); err != nil {
		return err, ""
	}
	return nil, r.Data.GriskID
}

// SetCaptchaSolver installs a captcha solving function. When set, voucher
// errors (x-bili-gaia-vvoucher) are resolved automatically inside
// WrapRoundTripFunc instead of being returned as errors.
func (c *BiliClient) SetCaptchaSolver(solver CaptchaSolverFn) {
	c.solver = solver
}

// HasCaptchaSolver returns whether a captcha solver has been installed.
func (c *BiliClient) HasCaptchaSolver() bool {
	return c.solver != nil
}

// resolveVoucher resolves a voucher string.
//
// The voucher is a URL like:
//
// Steps:
//  1. Extract the verify_key from the voucher URL.
//  2. POST to Bilibili's gaia verify endpoint to obtain geetest gt + challenge.
//  3. Solve the geetest captcha via the solver.
//  4. The returned validate is the resolved voucher token.
func (c *BiliClient) resolveVoucher(voucher string) (string, error) {
	if !c.HasCaptchaSolver() {
		return "", fmt.Errorf("no captcha solver installed")
	}
	err, token, gt, challenge := c.registerVoucher(voucher)
	if err != nil {
		return "", fmt.Errorf("failed to register voucher: %w", err)
	}
	validate, err := c.solver(gt, challenge)
	if err != nil {
		return "", fmt.Errorf("failed to solve captcha: %w", err)
	}
	err, griskID := c.vaildate(token, challenge, validate, gt)
	if err != nil {
		return "", fmt.Errorf("failed to validate voucher: %w", err)
	}
	return griskID, nil
}
