package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v2/database/model"
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
	var req struct {
		Client     model.Client `json:"client"`
		InboundIds []int        `json:"inboundIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
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
	var req struct {
		Client     model.Client `json:"client"`
		InboundIds []int        `json:"inboundIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
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
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.login.toasts.invalidFormData"), err)
		return
	}

	needRestart, err := a.clientService.ToggleEnableClient(email, req.Enable)
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
