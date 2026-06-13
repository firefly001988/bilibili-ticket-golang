package biliutils

import (
	"bilibili-ticket-golang/models/bili/api"
	"bilibili-ticket-golang/models/bili/captcha"
	"fmt"
)

func (c *BiliClient) registerVoucher(voucher string) (string, string, string, error) {
	res, err := c.client.R().SetFormData(map[string]string{
		"csrf":      c.getCSRFFromCookie(),
		"v_voucher": voucher,
	}).Post("https://api.bilibili.com/x/gaia-vgate/v1/register")
	if err != nil {
		return "", "", "", err
	}
	var r api.MainApiDataRoot[captcha.RegisterVoucherResponse]
	if err = res.Unmarshal(&r); err != nil {
		return "", "", "", err
	}
	if err = r.CheckValid(); err != nil {
		return "", "", "", err
	}
	if r.Data.Type != "geetest" {
		return "", "", "", fmt.Errorf("unexpected voucher type: %s", r.Data.Type)
	}
	return r.Data.Token, r.Data.Geetest.Gt, r.Data.Geetest.Challenge, nil
}

func (c *BiliClient) validate(token, challenge, validateVal, gt string) (string, error) {
	res, err := c.client.R().SetFormData(map[string]string{
		"csrf":      c.getCSRFFromCookie(),
		"challenge": challenge,
		"validate":  validateVal,
		"gt":        gt,
		"seccode":   validateVal + "|jordan",
	}).Post("https://api.bilibili.com/x/gaia-vgate/v1/validate")
	if err != nil {
		return "", err
	}
	var r api.MainApiDataRoot[captcha.ValidateResponse]
	if err = res.Unmarshal(&r); err != nil {
		return "", err
	}
	if err = r.CheckValid(); err != nil {
		return "", err
	}
	return r.Data.GriskID, nil
}

// SetCaptchaSolver installs a captcha solving function. When set, voucher
// errors (x-bili-gaia-vvoucher) are resolved automatically inside
// WrapRoundTripFunc instead of being returned as errors.
func (c *BiliClient) SetCaptchaSolver(solver CaptchaSolverFn) {
	c.solver.Store(&solver)
}

// HasCaptchaSolver returns whether a captcha solver has been installed.
func (c *BiliClient) HasCaptchaSolver() bool {
	ptr := c.solver.Load()
	return ptr != nil && *ptr != nil
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
	solverPtr := c.solver.Load()
	if solverPtr == nil || *solverPtr == nil {
		return "", fmt.Errorf("no captcha solver installed")
	}
	solver := *solverPtr
	token, gt, challenge, err := c.registerVoucher(voucher)
	if err != nil {
		return "", fmt.Errorf("failed to register voucher: %w", err)
	}
	validateVal, err := solver(gt, challenge)
	if err != nil {
		return "", fmt.Errorf("failed to solve captcha: %w", err)
	}
	griskID, err := c.validate(token, challenge, validateVal, gt)
	if err != nil {
		return "", fmt.Errorf("failed to validate voucher: %w", err)
	}
	return griskID, nil
}
