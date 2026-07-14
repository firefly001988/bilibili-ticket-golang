package api

type GetAxeResponse struct {
	Version   string `json:"version"`
	PublicKey string `json:"public_key"`
	Deadline  int64  `json:"deadline"`
}

type GaiaFingerprintOptions struct {
	SPMPrefix string
	PageURL   string
}

type GaiaFingerprintRequest struct {
	Payload string `json:"payload"`
}

type GaiaSecureFingerprintOptions struct {
	CollectAPI string
	PageURL    string
	SPMID      string
}

type GaiaPostLoginReportOptions struct {
	ExClimbWuzhi    GaiaFingerprintOptions
	ExClimbCongLing GaiaSecureFingerprintOptions
}

type GaiaSecureReportHeader struct {
	EncodeType     int    `json:"encode_type"`
	PayloadType    int    `json:"payload_type"`
	EncodedAESKey  string `json:"encoded_aes_key"`
	Timestamp      int64  `json:"ts"`
	EncodedVersion string `json:"encoded_version"`
}

type GaiaSecureReportRequest struct {
	Header         GaiaSecureReportHeader `json:"header"`
	EncryptPayload string                 `json:"encrypt_payload"`
}
