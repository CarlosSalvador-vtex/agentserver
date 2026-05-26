package server

import "net/http"

// This file holds thin per-route wrappers around s.imBridgeProxy so each
// wrapper can carry its own swag annotation block. The wrappers are wired
// up in server.go's router section when s.IMBridgeURL != "".
//
// The actual request handling all happens upstream in the imbridge service
// (see internal/imbridgesvc/); these wrappers exist only for OpenAPI
// documentation. Do NOT add per-route logic here — push it upstream.

// handleIMChannelList lists IM channels bound to a workspace.
//
//	@Summary   List IM channels in a workspace
//	@Tags      IM Channels
//	@Produce   json
//	@Param     id  path  string  true  "Workspace id"
//	@Success   200  {object}  IMChannelListResponse
//	@Failure   403  {string}  string  "not a member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels [get]
func (s *Server) handleIMChannelList(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMChannelPatch updates channel settings (require_mention and/or routing_mode).
//
//	@Summary   Update IM channel settings
//	@Tags      IM Channels
//	@Accept    json
//	@Produce   json
//	@Param     id         path  string                 true  "Workspace id"
//	@Param     channelId  path  string                 true  "Channel id"
//	@Param     body       body  IMChannelPatchRequest  true  "Settings patch"
//	@Success   200  {object}  IMChannelPatchResponse
//	@Failure   400  {string}  string  "bad request"
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "channel not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels/{channelId} [patch]
func (s *Server) handleIMChannelPatch(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMChannelDelete removes a channel binding and stops the associated poller.
//
//	@Summary   Delete an IM channel
//	@Tags      IM Channels
//	@Param     id         path  string  true  "Workspace id"
//	@Param     channelId  path  string  true  "Channel id"
//	@Success   204
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "channel not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels/{channelId} [delete]
func (s *Server) handleIMChannelDelete(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMWeixinQRStart starts the WeChat/Weixin QR-code login flow for a workspace.
//
//	@Summary     Start WeChat QR-code bind for a workspace
//	@Description Returns a QR code URL the user scans in WeChat. Client should then long-poll qr-wait until the channel is bound.
//	@Tags        IM Channels
//	@Produce     json
//	@Param       id  path  string  true  "Workspace id"
//	@Success     200  {object}  IMWeixinQRStartResponse
//	@Failure     403  {string}  string  "not a member"
//	@Failure     502  {string}  string  "upstream error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/weixin/qr-start [post]
func (s *Server) handleIMWeixinQRStart(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMWeixinQRWait long-polls for WeChat QR-code scan completion for a workspace.
//
//	@Summary     Long-poll WeChat QR-code scan for a workspace
//	@Description Polls for QR scan progress. Returns status "wait", "scaned", "confirmed", "expired", or other terminal states. On "confirmed", bot_id is set. On "expired", qrcode_url contains a refreshed code.
//	@Tags        IM Channels
//	@Produce     json
//	@Param       id  path  string  true  "Workspace id"
//	@Success     200  {object}  IMWeixinQRWaitResponse
//	@Failure     400  {string}  string  "no active session"
//	@Failure     403  {string}  string  "not a member"
//	@Failure     502  {string}  string  "upstream error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/weixin/qr-wait [post]
func (s *Server) handleIMWeixinQRWait(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMTelegramConfigure validates a Telegram bot token and binds a channel to the workspace.
//
//	@Summary     Bind a Telegram bot to a workspace
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                      true  "Workspace id"
//	@Param       body  body  IMTelegramConfigureRequest  true  "Bot token"
//	@Success     200   {object}  IMTelegramConfigureResponse
//	@Failure     400   {string}  string  "invalid bot token"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/telegram/configure [post]
func (s *Server) handleIMTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMMatrixConfigure validates Matrix credentials and binds a channel to the workspace.
//
//	@Summary     Bind a Matrix account to a workspace
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                    true  "Workspace id"
//	@Param       body  body  IMMatrixConfigureRequest  true  "Matrix credentials"
//	@Success     200   {object}  IMMatrixConfigureResponse
//	@Failure     400   {string}  string  "invalid credentials"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/matrix/configure [post]
func (s *Server) handleIMMatrixConfigure(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMWhatsAppConfigure binds a WhatsApp Cloud (Meta) Business
// phone number to a workspace. Unlike Telegram/Matrix this does not
// start a poller; inbound messages arrive at /webhook/whatsapp.
//
//	@Summary     Bind a WhatsApp Cloud number to a workspace
//	@Description Stores the Meta access token and phone_number_id. Inbound messages reach the agent via the public /webhook/whatsapp endpoint (configured in the Meta App dashboard).
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string  true  "Workspace id"
//	@Success     200   {object}  map[string]interface{}
//	@Failure     400   {string}  string  "phone_number_id and access_token are required"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/whatsapp/configure [post]
func (s *Server) handleIMWhatsAppConfigure(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMSandboxBind binds a sandbox to an existing workspace IM channel.
//
//	@Summary     Bind a sandbox to an IM channel
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                true  "Sandbox id"
//	@Param       body  body  IMSandboxBindRequest  true  "Channel id"
//	@Success     200   {object}  IMSandboxBindResponse
//	@Failure     400   {string}  string  "bad request"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     404   {string}  string  "sandbox or channel not found"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/im/bind [post]
func (s *Server) handleIMSandboxBind(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}

// handleIMSandboxUnbind removes a sandbox's IM channel binding.
//
//	@Summary     Unbind a sandbox from its IM channel
//	@Tags        IM Channels
//	@Produce     json
//	@Param       id  path  string  true  "Sandbox id"
//	@Success     200  {object}  IMSandboxUnbindResponse
//	@Failure     403  {string}  string  "not a member"
//	@Failure     404  {string}  string  "sandbox not found"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/im/bind [delete]
func (s *Server) handleIMSandboxUnbind(w http.ResponseWriter, r *http.Request) {
	s.imBridgeProxy(w, r)
}
