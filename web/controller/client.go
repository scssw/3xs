package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/web/service"
)

type ClientController struct {
	clientService  service.ClientService
	inboundService service.InboundService
	xrayService    service.XrayService
}

func NewClientController(g *gin.RouterGroup) *ClientController {
	a := &ClientController{}
	a.initRouter(g)
	return a
}

func (a *ClientController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.list)
	g.GET("/options", a.options)
	g.POST("/add", a.add)
	g.POST("/update/:email", a.update)
	g.POST("/del/:email", a.del)
	g.POST("/toggle/:email", a.toggle)
	g.POST("/resetTraffic/:email", a.resetTraffic)
	g.POST("/ips/:email", a.getIps)
	g.POST("/clearIps/:email", a.clearIps)
}

type clientReq struct {
	Client     model.Client `json:"client"`
	InboundIds []int        `json:"inboundIds"`
}

func bindClientReq(c *gin.Context) (*clientReq, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	trimmed := bytes.TrimSpace(bodyBytes)
	req := &clientReq{}

	// 1. Try parsing JSON body directly
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, req); err == nil && req.Client.Email != "" {
			return req, nil
		}
	}

	// 2. Parse URL-encoded form data (from axios / Qs.stringify)
	vals, err := url.ParseQuery(string(bodyBytes))
	if err != nil {
		return nil, err
	}

	// 2a. If "client" parameter is passed as a JSON string
	if clientJson := vals.Get("client"); clientJson != "" {
		trimmedJson := strings.TrimSpace(clientJson)
		if strings.HasPrefix(trimmedJson, "{") {
			_ = json.Unmarshal([]byte(trimmedJson), &req.Client)
		}
	}

	// 2b. Extract individual fields (both client[field] and field)
	getVal := func(field string) string {
		if v := vals.Get("client[" + field + "]"); v != "" {
			return v
		}
		return vals.Get(field)
	}

	if req.Client.Email == "" {
		req.Client.Email = strings.TrimSpace(getVal("email"))
	}
	if req.Client.SubID == "" {
		req.Client.SubID = strings.TrimSpace(getVal("subId"))
	}
	if req.Client.ID == "" {
		req.Client.ID = strings.TrimSpace(getVal("id"))
	}
	if req.Client.Password == "" {
		req.Client.Password = strings.TrimSpace(getVal("password"))
	}
	if req.Client.Auth == "" {
		req.Client.Auth = strings.TrimSpace(getVal("auth"))
	}
	if req.Client.Flow == "" {
		req.Client.Flow = strings.TrimSpace(getVal("flow"))
	}
	if req.Client.Comment == "" {
		req.Client.Comment = strings.TrimSpace(getVal("comment"))
	}
	if gbStr := getVal("totalGB"); gbStr != "" {
		if gb, err := strconv.ParseInt(gbStr, 10, 64); err == nil {
			req.Client.TotalGB = gb
		}
	}
	if expStr := getVal("expiryTime"); expStr != "" {
		if exp, err := strconv.ParseInt(expStr, 10, 64); err == nil {
			req.Client.ExpiryTime = exp
		}
	}
	if limitStr := getVal("limitIp"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			req.Client.LimitIP = limit
		}
	}
	if enableStr := getVal("enable"); enableStr != "" {
		req.Client.Enable = (enableStr == "true" || enableStr == "1")
	} else if len(trimmed) > 0 && !strings.Contains(string(bodyBytes), "enable") && req.Client.Email != "" {
		req.Client.Enable = true
	}

	// 2c. Parse inboundIds
	var rawIdStrs []string
	if ids, ok := vals["inboundIds"]; ok {
		rawIdStrs = append(rawIdStrs, ids...)
	}
	if ids, ok := vals["inboundIds[]"]; ok {
		rawIdStrs = append(rawIdStrs, ids...)
	}
	for k, v := range vals {
		if strings.HasPrefix(k, "inboundIds[") && len(v) > 0 {
			rawIdStrs = append(rawIdStrs, v[0])
		}
	}

	seen := make(map[int]bool)
	for _, idStr := range rawIdStrs {
		idStr = strings.TrimSpace(idStr)
		if strings.HasPrefix(idStr, "[") && strings.HasSuffix(idStr, "]") {
			var ids []int
			if json.Unmarshal([]byte(idStr), &ids) == nil {
				for _, id := range ids {
					if !seen[id] {
						seen[id] = true
						req.InboundIds = append(req.InboundIds, id)
					}
				}
				continue
			}
		}
		if id, err := strconv.Atoi(idStr); err == nil {
			if !seen[id] {
				seen[id] = true
				req.InboundIds = append(req.InboundIds, id)
			}
		}
	}

	if req.Client.Email == "" {
		return nil, common.NewError("client email is required")
	}
	return req, nil
}

func (a *ClientController) list(c *gin.Context) {
	summary, clients, err := a.clientService.GetClientsList()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "fail"), err)
		return
	}
	jsonObj(c, gin.H{
		"summary": summary,
		"clients": clients,
	}, nil)
}

func (a *ClientController) options(c *gin.Context) {
	options, err := a.clientService.GetInboundOptions()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "fail"), err)
		return
	}
	jsonObj(c, options, nil)
}

func (a *ClientController) add(c *gin.Context) {
	req, err := bindClientReq(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.login.toasts.invalidFormData"), err)
		return
	}

	needRestart, err := a.clientService.AddClient(&req.Client, req.InboundIds)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsg(c, I18nWeb(c, "success"), err)
}

func (a *ClientController) update(c *gin.Context) {
	email := c.Param("email")
	req, err := bindClientReq(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.login.toasts.invalidFormData"), err)
		return
	}

	needRestart, err := a.clientService.UpdateClient(email, &req.Client, req.InboundIds)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsg(c, I18nWeb(c, "success"), err)
}

func (a *ClientController) del(c *gin.Context) {
	email := c.Param("email")
	needRestart, err := a.clientService.DeleteClient(email)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsg(c, I18nWeb(c, "success"), err)
}

func (a *ClientController) toggle(c *gin.Context) {
	email := c.Param("email")
	bodyBytes, _ := io.ReadAll(c.Request.Body)
	trimmed := bytes.TrimSpace(bodyBytes)

	enable := false
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var req struct {
			Enable bool `json:"enable"`
		}
		if err := json.Unmarshal(trimmed, &req); err == nil {
			enable = req.Enable
		}
	} else {
		vals, _ := url.ParseQuery(string(bodyBytes))
		enableStr := vals.Get("enable")
		enable = (enableStr == "true" || enableStr == "1")
	}

	needRestart, err := a.clientService.ToggleEnableClient(email, enable)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsg(c, I18nWeb(c, "success"), err)
}

func (a *ClientController) resetTraffic(c *gin.Context) {
	email := c.Param("email")
	needRestart, err := a.clientService.ResetClientTraffic(email)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsg(c, I18nWeb(c, "success"), err)
}

func (a *ClientController) getIps(c *gin.Context) {
	email := c.Param("email")
	ips, err := a.inboundService.GetInboundClientIps(email)
	jsonObj(c, ips, err)
}

func (a *ClientController) clearIps(c *gin.Context) {
	email := c.Param("email")
	err := a.inboundService.ClearClientIps(email)
	jsonMsg(c, I18nWeb(c, "success"), err)
}
