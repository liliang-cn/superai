package backend

import (
	"sync"
	"time"
)

// What each channel last did, for the settings page: whether the last send
// went through, the last error, and how many messages came in today.
//
// Kept in memory for the life of the process, by channel id, outside the
// Service: a settings save builds a new Service, and the page should not
// forget a failure because something unrelated was saved.

// ChannelStatus is one channel's record.
type ChannelStatus struct {
	LastOKAt      string `json:"last_ok_at,omitempty"`
	LastErrorAt   string `json:"last_error_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	LastInboundAt string `json:"last_inbound_at,omitempty"`
	InboundToday  int    `json:"inbound_today"`

	inboundDay string
}

var channelStatuses = struct {
	sync.Mutex
	m map[string]*ChannelStatus
}{m: map[string]*ChannelStatus{}}

func statusOf(id string) *ChannelStatus {
	st := channelStatuses.m[id]
	if st == nil {
		st = &ChannelStatus{}
		channelStatuses.m[id] = st
	}
	return st
}

// RecordChannelSend notes a delivery's outcome.
func RecordChannelSend(id string, err error) {
	if id == "" {
		return
	}
	now := time.Now().Format(time.RFC3339)
	channelStatuses.Lock()
	defer channelStatuses.Unlock()
	st := statusOf(id)
	if err != nil {
		st.LastErrorAt, st.LastError = now, truncateRunes(err.Error(), 300)
		return
	}
	st.LastOKAt = now
}

// RecordChannelInbound counts a message that came in.
func RecordChannelInbound(id string) {
	now := time.Now()
	channelStatuses.Lock()
	defer channelStatuses.Unlock()
	st := statusOf(id)
	day := now.Format(time.DateOnly)
	if st.inboundDay != day {
		st.inboundDay, st.InboundToday = day, 0
	}
	st.InboundToday++
	st.LastInboundAt = now.Format(time.RFC3339)
}

// ChannelStatusOf returns a channel's record.
func ChannelStatusOf(id string) ChannelStatus {
	channelStatuses.Lock()
	defer channelStatuses.Unlock()
	st, ok := channelStatuses.m[id]
	if !ok {
		return ChannelStatus{}
	}
	out := *st
	if out.inboundDay != time.Now().Format(time.DateOnly) {
		out.InboundToday = 0
	}
	return out
}

// ForgetChannelStatus drops a deleted channel's record.
func ForgetChannelStatus(id string) {
	channelStatuses.Lock()
	delete(channelStatuses.m, id)
	channelStatuses.Unlock()
}
