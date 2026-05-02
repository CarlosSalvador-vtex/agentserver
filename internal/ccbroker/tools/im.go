package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

type sendMessageInput struct {
	Text   string `json:"text"`
	Sender string `json:"sender,omitempty"`
}
type sendImageInput struct {
	Source  string `json:"source"`
	Format  string `json:"format,omitempty"`
	Caption string `json:"caption,omitempty"`
}
type sendFileInput struct {
	Source   string `json:"source"`
	Filename string `json:"filename"`
	Caption  string `json:"caption,omitempty"`
}

func imTools(tctx *Context) []agentsdk.McpTool {
	return []agentsdk.McpTool{
		agentsdk.Tool[sendMessageInput]("send_message",
			"Send a text message to the user in the current IM conversation.",
			func(ctx context.Context, in sendMessageInput) (*agentsdk.McpToolResult, error) {
				return postIMSend(tctx, "text", map[string]any{
					"text": in.Text, "sender": in.Sender,
				})
			}),
		agentsdk.Tool[sendImageInput]("send_image",
			"Send an image to the user in the current IM conversation.",
			func(ctx context.Context, in sendImageInput) (*agentsdk.McpToolResult, error) {
				return postIMSend(tctx, "image", map[string]any{
					"source": in.Source, "format": in.Format, "caption": in.Caption,
				})
			}),
		agentsdk.Tool[sendFileInput]("send_file",
			"Send a file to the user in the current IM conversation.",
			func(ctx context.Context, in sendFileInput) (*agentsdk.McpToolResult, error) {
				return postIMSend(tctx, "file", map[string]any{
					"source": in.Source, "filename": in.Filename, "caption": in.Caption,
				})
			}),
	}
}

func postIMSend(tctx *Context, kind string, payload map[string]any) (*agentsdk.McpToolResult, error) {
	if tctx.IMChannelID == "" || tctx.IMUserID == "" {
		return errResult(fmt.Errorf("not invoked from an IM turn (missing channel_id or user_id)")), nil
	}
	body, _ := json.Marshal(map[string]any{
		"channel_id": tctx.IMChannelID,
		"user_id":    tctx.IMUserID,
		"kind":       kind,
		"payload":    payload,
	})
	req, _ := http.NewRequest("POST",
		tctx.AgentserverURL+"/api/internal/im/send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tctx.InternalAPISecret != "" {
		req.Header.Set("X-Internal-Secret", tctx.InternalAPISecret)
	}
	resp, err := tctx.HTTP.Do(req)
	if err != nil {
		return errResult(err), nil
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return errResult(fmt.Errorf("im/send %d: %s", resp.StatusCode, out)), nil
	}
	return textResult("sent"), nil
}
