package wechat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseIncomingText(t *testing.T) {
	body := []byte(`<xml>
<ToUserName><![CDATA[gh_abc]]></ToUserName>
<FromUserName><![CDATA[user_open_id]]></FromUserName>
<CreateTime>1717000000</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[你好春日]]></Content>
<MsgId>1234567890</MsgId>
</xml>`)
	msg, err := ParseIncoming(body)
	require.NoError(t, err)
	require.Equal(t, "gh_abc", msg.ToUserName)
	require.Equal(t, "user_open_id", msg.FromUserName)
	require.Equal(t, "text", msg.MsgType)
	require.Equal(t, "你好春日", msg.Content)
	require.Equal(t, "1234567890", msg.MsgID)
}
