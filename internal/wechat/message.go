package wechat

import "encoding/xml"

type IncomingMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
}

func ParseIncoming(body []byte) (*IncomingMessage, error) {
	var msg IncomingMessage
	if err := xml.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
