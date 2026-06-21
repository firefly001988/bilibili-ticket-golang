package errors

import (
	_return "bilibili-ticket-golang/lib/models/bili/response"
	"fmt"
)

type BilibiliAPIError struct {
	Code    int
	Message string
}

func NewBilibiliAPIError(code int, message string) *BilibiliAPIError {
	return &BilibiliAPIError{
		Code:    code,
		Message: message,
	}
}

func (bae *BilibiliAPIError) Error() string {
	return fmt.Sprintf("Response code is not 0, got: %d, message: %s", bae.Code, bae.Message)
}

type BilibiliAPIVoucherError struct {
	Voucher string
}

func NewBilibiliAPIVoucherError(voucher string) *BilibiliAPIVoucherError {
	return &BilibiliAPIVoucherError{
		Voucher: voucher,
	}
}

func (bav *BilibiliAPIVoucherError) Error() string {
	return fmt.Sprintf("Need voucher: %s", bav.Voucher)
}

type BilibiliMallTicketNotfoundError struct {
	ProjectID int64
	SkuID     int64
	ScreenID  int64
}

func NewBilibiliMallTicketNotfoundError(projectID, skuID, screenID int64) *BilibiliMallTicketNotfoundError {
	return &BilibiliMallTicketNotfoundError{
		ProjectID: projectID,
		SkuID:     skuID,
		ScreenID:  screenID,
	}
}
func (bmtn *BilibiliMallTicketNotfoundError) Error() string {
	return fmt.Sprintf("Ticket with skuID %d and screenID %d not found in project %d", bmtn.SkuID, bmtn.ScreenID, bmtn.ProjectID)
}

type BilibiliMallBuyerNotfoundError struct {
	Buyer _return.TicketBuyer
}

func NewBilibiliMallBuyerNotfoundError(buyer _return.TicketBuyer) *BilibiliMallBuyerNotfoundError {
	return &BilibiliMallBuyerNotfoundError{
		Buyer: buyer,
	}
}
func (bmbn *BilibiliMallBuyerNotfoundError) Error() string {
	return fmt.Sprintf("Buyer: %d(%s)(%s) not found", bmbn.Buyer.ID, bmbn.Buyer.Name, bmbn.Buyer.Tel)
}
