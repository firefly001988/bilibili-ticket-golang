package errors

import "fmt"

type CaptchaValidationError struct {
	Message string
}

func NewCaptchaValidationError(message string) *CaptchaValidationError {
	return &CaptchaValidationError{
		Message: message,
	}
}

func (cae *CaptchaValidationError) Error() string {
	return fmt.Sprintf("Captcha Vaildation error, message: %s", cae.Message)
}

type CaptchaTypeMismatchError struct {
	Current string
	Target  string
}

func NewCaptchaTypeMismatchError(current, target string) *CaptchaTypeMismatchError {
	return &CaptchaTypeMismatchError{
		Current: current,
		Target:  target,
	}
}

func (cte *CaptchaTypeMismatchError) Error() string {
	return fmt.Sprintf("Captcha type error, current: %s, target: %s", cte.Current, cte.Target)
}

type CaptchaInstanceDestroyedError struct{}

func NewCaptchaInstanceDestroyedError() *CaptchaInstanceDestroyedError {
	return &CaptchaInstanceDestroyedError{}
}

func (cide *CaptchaInstanceDestroyedError) Error() string {
	return "Captcha instance has been destroyed"
}
