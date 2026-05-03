package tui

import (
	"encoding/json"
)

// SSE-driven
type EventArrivedMsg struct{ Event SSEEvent }
type SSEStatusMsg    struct{ Status string; Reason string } // live | reconnecting | delayed

// HTTP-driven (Bus replies)
type InboundAcceptedMsg   struct{ SessionID, TurnID string }
type InboundRejectedMsg   struct{ Code, Message string }
type ControlReplyMsg      struct{ Command string; Body json.RawMessage; Err error }
type CancelReplyMsg       struct{ Err error }
type DecisionAckMsg       struct{ PermissionID string; Err error }
type AttachReplyMsg       struct{ Resp *AttachResponse; Err error }
type NewSessionReplyMsg   struct{ SessionID string; Err error }
type ListSessionsReplyMsg struct{ Sessions []SessionListItem; Err error }

// Periodic
type StatusTickMsg   struct{ Tunnel *ExecutorStatusResp; Err error }
type InitialStateMsg struct{ SessionID string; Model string; PermMode string }

// Auth
type AuthStateChangedMsg struct{ State AuthState }
type DeviceCodeReadyMsg  struct{ Info LoginInfo }
type LoginPollDoneMsg    struct{ Err error }
type LogoutDoneMsg       struct{ Err error }

// Internal user actions
type SendPromptMsg        struct{ Text string; Attachments []InboundAttachment; Metadata map[string]any }
type AttachmentPickedMsg  struct{ Attachment InboundAttachment }
type AttachmentRemovedMsg struct{ Index int }
type CommandSelectedMsg   struct{ Command, Args string }
type ResumeRequestedMsg   struct{ SessionID string }
type ClearRequestedMsg    struct{}

// Fatal
type FatalErrorMsg struct{ Err error }
