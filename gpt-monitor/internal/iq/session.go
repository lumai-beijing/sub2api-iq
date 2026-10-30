package iq

import (
	"crypto/rand"
	"encoding/hex"
)

// 第三方号池的粘性路由会把「同一个会话」固定在同一个上游账号上，一般保持 1 小时、命中就续期。
// 检测每几分钟发一次，不处理的话会一直被认成同一个会话，测来测去都是同一个号，
// 测出来的是那一个号的智商，不是这个供应商的平均水平。
//
// 常见中转判定会话的依据（sub2api、CLIProxyAPI、one-api 系实测/读代码）：
//  1. 显式会话 ID：请求头 session_id / Session-Id / X-Session-Id / conversation_id，或请求体 prompt_cache_key；
//  2. 都没有时，按内容兜底：模型 + 系统提示 + 第一条用户消息算哈希。
//
// 第 1 条：每次检测带一个全新的随机会话 ID。
// 第 2 条：题面本身每次都随机改写（variant.go），第一条用户消息每次都不同，内容哈希也就每次不同。

// probeSession 是一次检测的会话标识。
type probeSession struct {
	ID string // UUID v4，作为会话 ID 发出去
}

func newProbeSession() probeSession {
	return probeSession{ID: randomUUID()}
}

// sessionHeaders 是各家中转认的会话头。都是普通的自定义头，不认的中转会直接忽略。
// 刻意不发 X-Client-Request-Id：有的实现把它当「每条请求」而不是「每个会话」的 ID，语义不对。
var sessionHeaders = []string{"session_id", "Session-Id", "X-Session-Id", "conversation_id"}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
