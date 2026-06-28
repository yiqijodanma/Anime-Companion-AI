package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type APIError struct {
	ErrCode int
	ErrMsg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wechat api error %d: %s", e.ErrCode, e.ErrMsg)
}

func IsTokenExpiredError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.ErrCode == 40001 || apiErr.ErrCode == 42001)
}

type KFClient struct {
	client   *http.Client
	Endpoint string
}

func NewKFClient(client *http.Client) *KFClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &KFClient{
		client:   client,
		Endpoint: "https://api.weixin.qq.com/cgi-bin/message/custom/send",
	}
}

type kfResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (c *KFClient) SendText(ctx context.Context, token, openID, text string) error {
	payload := map[string]any{
		"touser":  openID,
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"?access_token="+token, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var kr kfResp
	if err := json.Unmarshal(body, &kr); err != nil {
		return err
	}
	if kr.ErrCode != 0 {
		return &APIError{ErrCode: kr.ErrCode, ErrMsg: kr.ErrMsg}
	}
	return nil
}
