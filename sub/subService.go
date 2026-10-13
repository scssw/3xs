package sub

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/util/random"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// SubService provides business logic for generating subscription links and managing subscription data.
type SubService struct {
	address        string
	showInfo       bool
	remarkModel    string
	datepicker     string
	inboundService service.InboundService
	settingService service.SettingService
}

// NewSubService creates a new subscription service with the given configuration.
func NewSubService(showInfo bool, remarkModel string) *SubService {
	return &SubService{
		showInfo:    showInfo,
		remarkModel: remarkModel,
	}
}

// GetSubs retrieves subscription links for a given subscription ID and host.
func (s *SubService) GetSubs(subId string, host string) ([]string, int64, xray.ClientTraffic, error) {
	s.address = host
	var result []string
	var traffic xray.ClientTraffic
	var lastOnline int64
	var hasEnabledClient bool
	var clientTraffics []xray.ClientTraffic
	inbounds, err := s.getInboundsBySubId(subId)
	if err != nil {
		return nil, 0, traffic, err
	}

	if len(inbounds) == 0 {
		return nil, 0, traffic, common.NewError("No inbounds found with ", subId)
	}

	s.datepicker, err = s.settingService.GetDatepicker()
	if err != nil {
		s.datepicker = "gregorian"
	}
	seenEmails := make(map[string]bool)
	for _, inbound := range inbounds {
		clients, err := s.inboundService.GetClients(inbound)
		if err != nil {
			logger.Error("SubService - GetClients: Unable to get clients from inbound")
		}
		if clients == nil {
			continue
		}
		if len(inbound.Listen) > 0 && inbound.Listen[0] == '@' {
			listen, port, streamSettings, err := s.getFallbackMaster(inbound.Listen, inbound.StreamSettings)
			if err == nil {
				inbound.Listen = listen
				inbound.Port = port
				inbound.StreamSettings = streamSettings
			}
		}
		for _, client := range clients {
			if client.SubID == subId {
				if client.Enable {
					hasEnabledClient = true
				}
				if !client.Enable {
					continue
				}
				link := s.getLink(inbound, client.Email)
				result = append(result, link)
				if !seenEmails[client.Email] {
					seenEmails[client.Email] = true
					ct := s.getClientTraffics(inbound.ClientStats, client.Email)
					if ct.Email == "" || (ct.Total == 0 && ct.ExpiryTime == 0 && (client.TotalGB > 0 || client.ExpiryTime > 0)) {
						ct.Email = client.Email
						tot := client.TotalGB
						if tot > 0 && tot < 1024*1024 {
							tot = tot * 1024 * 1024 * 1024
						}
						if ct.Total == 0 {
							ct.Total = tot
						}
						if ct.ExpiryTime == 0 {
							ct.ExpiryTime = client.ExpiryTime
						}
						ct.Enable = client.Enable
					}
					clientTraffics = append(clientTraffics, ct)
					if ct.LastOnline > lastOnline {
						lastOnline = ct.LastOnline
					}
				}
			}
		}
	}

	// Prepare statistics
	for index, clientTraffic := range clientTraffics {
		if index == 0 {
			traffic.Up = clientTraffic.Up
			traffic.Down = clientTraffic.Down
			traffic.Total = clientTraffic.Total
			if clientTraffic.ExpiryTime > 0 {
				traffic.ExpiryTime = clientTraffic.ExpiryTime
			}
		} else {
			traffic.Up += clientTraffic.Up
			traffic.Down += clientTraffic.Down
			if traffic.Total == 0 || clientTraffic.Total == 0 {
				traffic.Total = 0
			} else {
				traffic.Total += clientTraffic.Total
			}
			if clientTraffic.ExpiryTime != traffic.ExpiryTime {
				traffic.ExpiryTime = 0
			}
		}
	}
	traffic.Enable = hasEnabledClient
	return result, lastOnline, traffic, nil
}

func (s *SubService) getInboundsBySubId(subId string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where(`id in (
		SELECT DISTINCT inbounds.id
		FROM inbounds,
			JSON_EACH(JSON_EXTRACT(inbounds.settings, '$.clients')) AS client 
		WHERE
			protocol in ('vmess','vless','trojan','shadowsocks','hysteria','hysteria2')
			AND JSON_EXTRACT(client.value, '$.subId') = ? AND enable = ?
	)`, subId, true).Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	return inbounds, nil
}

func (s *SubService) getClientTraffics(traffics []xray.ClientTraffic, email string) xray.ClientTraffic {
	for _, traffic := range traffics {
		if traffic.Email == email {
			return traffic
		}
	}
	if email != "" {
		if ct, err := s.inboundService.GetClientTrafficByEmail(email); err == nil && ct != nil {
			return *ct
		}
	}
	return xray.ClientTraffic{}
}

func (s *SubService) getBoundAddress() string {
	address, err := s.settingService.GetInboundBindAddress()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(address)
}

func (s *SubService) resolveAddress(inbound *model.Inbound) string {
	if address := s.getBoundAddress(); address != "" {
		return address
	}
	if inbound.Listen == "" || inbound.Listen == "0.0.0.0" || inbound.Listen == "::" || inbound.Listen == "::0" {
		return s.address
	}
	return inbound.Listen
}

func (s *SubService) resolveExternalAddress(dest string) string {
	if address := s.getBoundAddress(); address != "" {
		return address
	}
	return dest
}

func (s *SubService) getFallbackMaster(dest string, streamSettings string) (string, int, string, error) {
	db := database.GetDB()
	var inbound *model.Inbound
	err := db.Model(model.Inbound{}).
		Where("JSON_TYPE(settings, '$.fallbacks') = 'array'").
		Where("EXISTS (SELECT * FROM json_each(settings, '$.fallbacks') WHERE json_extract(value, '$.dest') = ?)", dest).
		Find(&inbound).Error
	if err != nil {
		return "", 0, "", err
	}

	var stream map[string]any
	json.Unmarshal([]byte(streamSettings), &stream)
	var masterStream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &masterStream)
	stream["security"] = masterStream["security"]
	stream["tlsSettings"] = masterStream["tlsSettings"]
	stream["externalProxy"] = masterStream["externalProxy"]
	modifiedStream, _ := json.MarshalIndent(stream, "", "  ")

	return inbound.Listen, inbound.Port, string(modifiedStream), nil
}

func (s *SubService) getLink(inbound *model.Inbound, email string) string {
	switch inbound.Protocol {
	case "vmess":
		return s.genVmessLink(inbound, email)
	case "vless":
		return s.genVlessLink(inbound, email)
	case "trojan":
		return s.genTrojanLink(inbound, email)
	case "shadowsocks":
		return s.genShadowsocksLink(inbound, email)
	case "hysteria", "hysteria2":
		return s.genHysteriaLink(inbound, email)
	}
	return ""
}

func (s *SubService) genVmessLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.VMESS {
		return ""
	}
	address := s.resolveAddress(inbound)
	obj := map[string]any{
		"v":    "2",
		"add":  address,
		"port": inbound.Port,
		"type": "none",
	}
	var stream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	network, _ := stream["network"].(string)
	obj["net"] = network
	switch network {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		obj["type"] = typeStr
		if typeStr == "http" {
			request := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			obj["path"] = requestPath[0].(string)
			headers, _ := request["headers"].(map[string]any)
			obj["host"] = searchHost(headers)
		}
	case "kcp":
		kcp, _ := stream["kcpSettings"].(map[string]any)
		header, _ := kcp["header"].(map[string]any)
		obj["type"], _ = header["type"].(string)
		obj["path"], _ = kcp["seed"].(string)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		obj["path"] = ws["path"].(string)
		if host, ok := ws["host"].(string); ok && len(host) > 0 {
			obj["host"] = host
		} else {
			headers, _ := ws["headers"].(map[string]any)
			obj["host"] = searchHost(headers)
		}
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		obj["path"] = grpc["serviceName"].(string)
		obj["authority"] = grpc["authority"].(string)
		if grpc["multiMode"].(bool) {
			obj["type"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		obj["path"] = httpupgrade["path"].(string)
		if host, ok := httpupgrade["host"].(string); ok && len(host) > 0 {
			obj["host"] = host
		} else {
			headers, _ := httpupgrade["headers"].(map[string]any)
			obj["host"] = searchHost(headers)
		}
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		obj["path"] = xhttp["path"].(string)
		if host, ok := xhttp["host"].(string); ok && len(host) > 0 {
			obj["host"] = host
		} else {
			headers, _ := xhttp["headers"].(map[string]any)
			obj["host"] = searchHost(headers)
		}
		obj["mode"] = xhttp["mode"].(string)
	}
	security, _ := stream["security"].(string)
	obj["tls"] = security
	if finalmask, ok := stream["finalmask"].(map[string]any); ok {
		applyFinalMaskObj(finalmask, obj)
	}
	if security == "tls" {
		tlsSetting, _ := stream["tlsSettings"].(map[string]any)
		alpns, _ := tlsSetting["alpn"].([]any)
		if len(alpns) > 0 {
			var alpn []string
			for _, a := range alpns {
				alpn = append(alpn, a.(string))
			}
			obj["alpn"] = strings.Join(alpn, ",")
		}
		if sniValue, ok := searchKey(tlsSetting, "serverName"); ok {
			obj["sni"], _ = sniValue.(string)
		}

		tlsSettings, _ := searchKey(tlsSetting, "settings")
		if tlsSetting != nil {
			if fpValue, ok := searchKey(tlsSettings, "fingerprint"); ok {
				obj["fp"], _ = fpValue.(string)
			}
			if insecure, ok := searchKey(tlsSettings, "allowInsecure"); ok {
				obj["allowInsecure"], _ = insecure.(bool)
			}
		}
	}

	clients, _ := s.inboundService.GetClients(inbound)
	clientIndex := -1
	for i, client := range clients {
		if client.Email == email {
			clientIndex = i
			break
		}
	}
	obj["id"] = clients[clientIndex].ID
	obj["scy"] = clients[clientIndex].Security

	externalProxies, _ := stream["externalProxy"].([]any)

	if len(externalProxies) > 0 {
		links := ""
		for index, externalProxy := range externalProxies {
			ep, _ := externalProxy.(map[string]any)
			newSecurity, _ := ep["forceTls"].(string)
			newObj := map[string]any{}
			for key, value := range obj {
				if !(newSecurity == "none" && (key == "alpn" || key == "sni" || key == "fp" || key == "allowInsecure")) {
					newObj[key] = value
				}
			}
			newObj["ps"] = s.genRemark(inbound, email, ep["remark"].(string))
			newObj["add"] = s.resolveExternalAddress(ep["dest"].(string))
			newObj["port"] = int(ep["port"].(float64))

			if newSecurity != "same" {
				newObj["tls"] = newSecurity
			}
			if index > 0 {
				links += "\n"
			}
			jsonStr, _ := json.MarshalIndent(newObj, "", "  ")
			links += "vmess://" + base64.StdEncoding.EncodeToString(jsonStr)
		}
		return links
	}

	obj["ps"] = s.genRemark(inbound, email, "")

	jsonStr, _ := json.MarshalIndent(obj, "", "  ")
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonStr)
}

func (s *SubService) genVlessLink(inbound *model.Inbound, email string) string {
	address := s.resolveAddress(inbound)

	if inbound.Protocol != model.VLESS {
		return ""
	}
	var stream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	clients, _ := s.inboundService.GetClients(inbound)
	clientIndex := -1
	for i, client := range clients {
		if client.Email == email {
			clientIndex = i
			break
		}
	}
	uuid := clients[clientIndex].ID
	port := inbound.Port
	streamNetwork := stream["network"].(string)
	params := make(map[string]string)
	params["type"] = streamNetwork

	// Add encryption parameter for VLESS from inbound settings
	var settings map[string]any
	json.Unmarshal([]byte(inbound.Settings), &settings)
	if encryption, ok := settings["encryption"].(string); ok {
		params["encryption"] = encryption
	}

	switch streamNetwork {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		if typeStr == "http" {
			request := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			params["path"] = requestPath[0].(string)
			headers, _ := request["headers"].(map[string]any)
			params["host"] = searchHost(headers)
			params["headerType"] = "http"
		}
	case "kcp":
		kcp, _ := stream["kcpSettings"].(map[string]any)
		header, _ := kcp["header"].(map[string]any)
		params["headerType"] = header["type"].(string)
		params["seed"] = kcp["seed"].(string)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		params["path"] = ws["path"].(string)
		if host, ok := ws["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := ws["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		params["serviceName"] = grpc["serviceName"].(string)
		params["authority"], _ = grpc["authority"].(string)
		if grpc["multiMode"].(bool) {
			params["mode"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		params["path"] = httpupgrade["path"].(string)
		if host, ok := httpupgrade["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := httpupgrade["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		params["path"] = xhttp["path"].(string)
		if host, ok := xhttp["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := xhttp["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
		params["mode"] = xhttp["mode"].(string)
	}
	security, _ := stream["security"].(string)
	if finalmask, ok := stream["finalmask"].(map[string]any); ok {
		applyFinalMaskParams(finalmask, params)
	}
	if security == "tls" {
		params["security"] = "tls"
		tlsSetting, _ := stream["tlsSettings"].(map[string]any)
		alpns, _ := tlsSetting["alpn"].([]any)
		var alpn []string
		for _, a := range alpns {
			alpn = append(alpn, a.(string))
		}
		if len(alpn) > 0 {
			params["alpn"] = strings.Join(alpn, ",")
		}
		if sniValue, ok := searchKey(tlsSetting, "serverName"); ok {
			params["sni"], _ = sniValue.(string)
		}

		tlsSettings, _ := searchKey(tlsSetting, "settings")
		if tlsSetting != nil {
			if fpValue, ok := searchKey(tlsSettings, "fingerprint"); ok {
				params["fp"], _ = fpValue.(string)
			}
			if insecure, ok := searchKey(tlsSettings, "allowInsecure"); ok {
				if insecure.(bool) {
					params["allowInsecure"] = "1"
				}
			}
		}

		if streamNetwork == "tcp" && len(clients[clientIndex].Flow) > 0 {
			params["flow"] = clients[clientIndex].Flow
		}
	}

	if security == "reality" {
		params["security"] = "reality"
		realitySetting, _ := stream["realitySettings"].(map[string]any)
		realitySettings, _ := searchKey(realitySetting, "settings")
		if realitySetting != nil {
			if sniValue, ok := searchKey(realitySetting, "serverNames"); ok {
				sNames, _ := sniValue.([]any)
				params["sni"] = sNames[random.Num(len(sNames))].(string)
			}
			if pbkValue, ok := searchKey(realitySettings, "publicKey"); ok {
				params["pbk"], _ = pbkValue.(string)
			}
			if sidValue, ok := searchKey(realitySetting, "shortIds"); ok {
				shortIds, _ := sidValue.([]any)
				params["sid"] = shortIds[random.Num(len(shortIds))].(string)
			}
			if fpValue, ok := searchKey(realitySettings, "fingerprint"); ok {
				if fp, ok := fpValue.(string); ok && len(fp) > 0 {
					params["fp"] = fp
				}
			}
			if pqvValue, ok := searchKey(realitySettings, "mldsa65Verify"); ok {
				if pqv, ok := pqvValue.(string); ok && len(pqv) > 0 {
					params["pqv"] = pqv
				}
			}
			if spxValue, ok := searchKey(realitySettings, "spiderX"); ok {
				if spx, ok := spxValue.(string); ok && len(spx) > 0 {
					params["spx"] = spx
				}
			}
			if _, ok := params["spx"]; !ok {
				params["spx"] = "/" + random.Seq(15)
			}
		}

		if streamNetwork == "tcp" && len(clients[clientIndex].Flow) > 0 {
			params["flow"] = clients[clientIndex].Flow
		}
	}

	if security != "tls" && security != "reality" {
		params["security"] = "none"
	}

	externalProxies, _ := stream["externalProxy"].([]any)

	if len(externalProxies) > 0 {
		links := make([]string, 0, len(externalProxies))
		for _, externalProxy := range externalProxies {
			ep, _ := externalProxy.(map[string]any)
			newSecurity, _ := ep["forceTls"].(string)
			dest, _ := ep["dest"].(string)
			dest = s.resolveExternalAddress(dest)
			port := int(ep["port"].(float64))
			link := fmt.Sprintf("vless://%s@%s:%d", uuid, dest, port)

			if newSecurity != "same" {
				params["security"] = newSecurity
			} else {
				params["security"] = security
			}
			url, _ := url.Parse(link)
			q := url.Query()

			for k, v := range params {
				if !(newSecurity == "none" && (k == "alpn" || k == "sni" || k == "fp" || k == "allowInsecure")) {
					q.Add(k, v)
				}
			}

			// Set the new query values on the URL
			url.RawQuery = q.Encode()

			url.Fragment = s.genRemark(inbound, email, ep["remark"].(string))

			links = append(links, url.String())
		}
		return strings.Join(links, "\n")
	}

	link := fmt.Sprintf("vless://%s@%s:%d", uuid, address, port)
	url, _ := url.Parse(link)
	q := url.Query()

	for k, v := range params {
		q.Add(k, v)
	}

	// Set the new query values on the URL
	url.RawQuery = q.Encode()

	url.Fragment = s.genRemark(inbound, email, "")
	return url.String()
}

func (s *SubService) genTrojanLink(inbound *model.Inbound, email string) string {
	address := s.resolveAddress(inbound)
	if inbound.Protocol != model.Trojan {
		return ""
	}
	var stream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	clients, _ := s.inboundService.GetClients(inbound)
	clientIndex := -1
	for i, client := range clients {
		if client.Email == email {
			clientIndex = i
			break
		}
	}
	password := clients[clientIndex].Password
	port := inbound.Port
	streamNetwork := stream["network"].(string)
	params := make(map[string]string)
	params["type"] = streamNetwork

	switch streamNetwork {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		if typeStr == "http" {
			request := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			params["path"] = requestPath[0].(string)
			headers, _ := request["headers"].(map[string]any)
			params["host"] = searchHost(headers)
			params["headerType"] = "http"
		}
	case "kcp":
		kcp, _ := stream["kcpSettings"].(map[string]any)
		header, _ := kcp["header"].(map[string]any)
		params["headerType"] = header["type"].(string)
		params["seed"] = kcp["seed"].(string)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		params["path"] = ws["path"].(string)
		if host, ok := ws["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := ws["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		params["serviceName"] = grpc["serviceName"].(string)
		params["authority"], _ = grpc["authority"].(string)
		if grpc["multiMode"].(bool) {
			params["mode"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		params["path"] = httpupgrade["path"].(string)
		if host, ok := httpupgrade["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := httpupgrade["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		params["path"] = xhttp["path"].(string)
		if host, ok := xhttp["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := xhttp["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
		params["mode"] = xhttp["mode"].(string)
	}
	if finalmask, ok := stream["finalmask"].(map[string]any); ok {
		applyFinalMaskParams(finalmask, params)
	}
	security, _ := stream["security"].(string)
	if security == "tls" {
		params["security"] = "tls"
		tlsSetting, _ := stream["tlsSettings"].(map[string]any)
		alpns, _ := tlsSetting["alpn"].([]any)
		var alpn []string
		for _, a := range alpns {
			alpn = append(alpn, a.(string))
		}
		if len(alpn) > 0 {
			params["alpn"] = strings.Join(alpn, ",")
		}
		if sniValue, ok := searchKey(tlsSetting, "serverName"); ok {
			params["sni"], _ = sniValue.(string)
		}

		tlsSettings, _ := searchKey(tlsSetting, "settings")
		if tlsSetting != nil {
			if fpValue, ok := searchKey(tlsSettings, "fingerprint"); ok {
				params["fp"], _ = fpValue.(string)
			}
			if insecure, ok := searchKey(tlsSettings, "allowInsecure"); ok {
				if insecure.(bool) {
					params["allowInsecure"] = "1"
				}
			}
		}
	}

	if security == "reality" {
		params["security"] = "reality"
		realitySetting, _ := stream["realitySettings"].(map[string]any)
		realitySettings, _ := searchKey(realitySetting, "settings")
		if realitySetting != nil {
			if sniValue, ok := searchKey(realitySetting, "serverNames"); ok {
				sNames, _ := sniValue.([]any)
				params["sni"] = sNames[random.Num(len(sNames))].(string)
			}
			if pbkValue, ok := searchKey(realitySettings, "publicKey"); ok {
				params["pbk"], _ = pbkValue.(string)
			}
			if sidValue, ok := searchKey(realitySetting, "shortIds"); ok {
				shortIds, _ := sidValue.([]any)
				params["sid"] = shortIds[random.Num(len(shortIds))].(string)
			}
			if fpValue, ok := searchKey(realitySettings, "fingerprint"); ok {
				if fp, ok := fpValue.(string); ok && len(fp) > 0 {
					params["fp"] = fp
				}
			}
			if pqvValue, ok := searchKey(realitySettings, "mldsa65Verify"); ok {
				if pqv, ok := pqvValue.(string); ok && len(pqv) > 0 {
					params["pqv"] = pqv
				}
			}
			params["spx"] = "/" + random.Seq(15)
		}

		if streamNetwork == "tcp" && len(clients[clientIndex].Flow) > 0 {
			params["flow"] = clients[clientIndex].Flow
		}
	}

	if security != "tls" && security != "reality" {
		params["security"] = "none"
	}

	externalProxies, _ := stream["externalProxy"].([]any)

	if len(externalProxies) > 0 {
		links := ""
		for index, externalProxy := range externalProxies {
			ep, _ := externalProxy.(map[string]any)
			newSecurity, _ := ep["forceTls"].(string)
			dest, _ := ep["dest"].(string)
			dest = s.resolveExternalAddress(dest)
			port := int(ep["port"].(float64))
			link := fmt.Sprintf("trojan://%s@%s:%d", password, dest, port)

			if newSecurity != "same" {
				params["security"] = newSecurity
			} else {
				params["security"] = security
			}
			url, _ := url.Parse(link)
			q := url.Query()

			for k, v := range params {
				if !(newSecurity == "none" && (k == "alpn" || k == "sni" || k == "fp" || k == "allowInsecure")) {
					q.Add(k, v)
				}
			}

			// Set the new query values on the URL
			url.RawQuery = q.Encode()

			url.Fragment = s.genRemark(inbound, email, ep["remark"].(string))

			if index > 0 {
				links += "\n"
			}
			links += url.String()
		}
		return links
	}

	link := fmt.Sprintf("trojan://%s@%s:%d", password, address, port)

	url, _ := url.Parse(link)
	q := url.Query()

	for k, v := range params {
		q.Add(k, v)
	}

	// Set the new query values on the URL
	url.RawQuery = q.Encode()

	url.Fragment = s.genRemark(inbound, email, "")
	return url.String()
}

func (s *SubService) genShadowsocksLink(inbound *model.Inbound, email string) string {
	address := s.resolveAddress(inbound)
	if inbound.Protocol != model.Shadowsocks {
		return ""
	}
	var stream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	clients, _ := s.inboundService.GetClients(inbound)

	var settings map[string]any
	json.Unmarshal([]byte(inbound.Settings), &settings)
	inboundPassword := settings["password"].(string)
	method := settings["method"].(string)
	clientIndex := -1
	for i, client := range clients {
		if client.Email == email {
			clientIndex = i
			break
		}
	}
	streamNetwork := stream["network"].(string)
	params := make(map[string]string)
	params["type"] = streamNetwork

	switch streamNetwork {
	case "tcp":
		tcp, _ := stream["tcpSettings"].(map[string]any)
		header, _ := tcp["header"].(map[string]any)
		typeStr, _ := header["type"].(string)
		if typeStr == "http" {
			request := header["request"].(map[string]any)
			requestPath, _ := request["path"].([]any)
			params["path"] = requestPath[0].(string)
			headers, _ := request["headers"].(map[string]any)
			params["host"] = searchHost(headers)
			params["headerType"] = "http"
		}
	case "kcp":
		kcp, _ := stream["kcpSettings"].(map[string]any)
		header, _ := kcp["header"].(map[string]any)
		params["headerType"] = header["type"].(string)
		params["seed"] = kcp["seed"].(string)
	case "ws":
		ws, _ := stream["wsSettings"].(map[string]any)
		params["path"] = ws["path"].(string)
		if host, ok := ws["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := ws["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "grpc":
		grpc, _ := stream["grpcSettings"].(map[string]any)
		params["serviceName"] = grpc["serviceName"].(string)
		params["authority"], _ = grpc["authority"].(string)
		if grpc["multiMode"].(bool) {
			params["mode"] = "multi"
		}
	case "httpupgrade":
		httpupgrade, _ := stream["httpupgradeSettings"].(map[string]any)
		params["path"] = httpupgrade["path"].(string)
		if host, ok := httpupgrade["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := httpupgrade["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
	case "xhttp":
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		params["path"] = xhttp["path"].(string)
		if host, ok := xhttp["host"].(string); ok && len(host) > 0 {
			params["host"] = host
		} else {
			headers, _ := xhttp["headers"].(map[string]any)
			params["host"] = searchHost(headers)
		}
		params["mode"] = xhttp["mode"].(string)
	}

	if finalmask, ok := stream["finalmask"].(map[string]any); ok {
		applyFinalMaskParams(finalmask, params)
	}

	security, _ := stream["security"].(string)
	if security == "tls" {
		params["security"] = "tls"
		tlsSetting, _ := stream["tlsSettings"].(map[string]any)
		alpns, _ := tlsSetting["alpn"].([]any)
		var alpn []string
		for _, a := range alpns {
			alpn = append(alpn, a.(string))
		}
		if len(alpn) > 0 {
			params["alpn"] = strings.Join(alpn, ",")
		}
		if sniValue, ok := searchKey(tlsSetting, "serverName"); ok {
			params["sni"], _ = sniValue.(string)
		}

		tlsSettings, _ := searchKey(tlsSetting, "settings")
		if tlsSetting != nil {
			if fpValue, ok := searchKey(tlsSettings, "fingerprint"); ok {
				params["fp"], _ = fpValue.(string)
			}
			if insecure, ok := searchKey(tlsSettings, "allowInsecure"); ok {
				if insecure.(bool) {
					params["allowInsecure"] = "1"
				}
			}
		}
	}

	encPart := fmt.Sprintf("%s:%s", method, clients[clientIndex].Password)
	if method[0] == '2' {
		encPart = fmt.Sprintf("%s:%s:%s", method, inboundPassword, clients[clientIndex].Password)
	}

	externalProxies, _ := stream["externalProxy"].([]any)

	if len(externalProxies) > 0 {
		links := ""
		for index, externalProxy := range externalProxies {
			ep, _ := externalProxy.(map[string]any)
			newSecurity, _ := ep["forceTls"].(string)
			dest, _ := ep["dest"].(string)
			dest = s.resolveExternalAddress(dest)
			port := int(ep["port"].(float64))
			link := fmt.Sprintf("ss://%s@%s:%d", base64.StdEncoding.EncodeToString([]byte(encPart)), dest, port)

			if newSecurity != "same" {
				params["security"] = newSecurity
			} else {
				params["security"] = security
			}
			url, _ := url.Parse(link)
			q := url.Query()

			for k, v := range params {
				if !(newSecurity == "none" && (k == "alpn" || k == "sni" || k == "fp" || k == "allowInsecure")) {
					q.Add(k, v)
				}
			}

			// Set the new query values on the URL
			url.RawQuery = q.Encode()

			url.Fragment = s.genRemark(inbound, email, ep["remark"].(string))

			if index > 0 {
				links += "\n"
			}
			links += url.String()
		}
		return links
	}

	link := fmt.Sprintf("ss://%s@%s:%d", base64.StdEncoding.EncodeToString([]byte(encPart)), address, inbound.Port)
	url, _ := url.Parse(link)
	q := url.Query()

	for k, v := range params {
		q.Add(k, v)
	}

	// Set the new query values on the URL
	url.RawQuery = q.Encode()

	url.Fragment = s.genRemark(inbound, email, "")
	return url.String()
}

func (s *SubService) genHysteriaLink(inbound *model.Inbound, email string) string {
	if !model.IsHysteria(inbound.Protocol) {
		return ""
	}

	var stream map[string]any
	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	clients, _ := s.inboundService.GetClients(inbound)
	clientIndex := -1
	for i, client := range clients {
		if client.Email == email {
			clientIndex = i
			break
		}
	}
	if clientIndex < 0 {
		return ""
	}

	auth := clients[clientIndex].Auth
	params := make(map[string]string)
	params["security"] = "tls"

	tlsSetting, _ := stream["tlsSettings"].(map[string]any)
	if tlsSetting != nil {
		if alpns, ok := tlsSetting["alpn"].([]any); ok {
			alpn := make([]string, 0, len(alpns))
			for _, a := range alpns {
				if v, ok := a.(string); ok && v != "" {
					alpn = append(alpn, v)
				}
			}
			if len(alpn) > 0 {
				params["alpn"] = strings.Join(alpn, ",")
			}
		}
		if sniValue, ok := searchKey(tlsSetting, "serverName"); ok {
			params["sni"], _ = sniValue.(string)
		}
		tlsSettings, _ := searchKey(tlsSetting, "settings")
		if fpValue, ok := searchKey(tlsSettings, "fingerprint"); ok {
			params["fp"], _ = fpValue.(string)
		}
		if insecure, ok := searchKey(tlsSettings, "allowInsecure"); ok {
			if v, ok := insecure.(bool); ok && v {
				params["insecure"] = "1"
			}
		}
	}

	if finalmask, ok := stream["finalmask"].(map[string]any); ok {
		if udpMasks, ok := finalmask["udp"].([]any); ok {
			for _, rawMask := range udpMasks {
				mask, _ := rawMask.(map[string]any)
				if mask == nil {
					continue
				}
				maskType, _ := mask["type"].(string)
				settings, _ := mask["settings"].(map[string]any)
				password, _ := settings["password"].(string)
				if maskType == "salamander" && password != "" {
					params["obfs"] = "salamander"
					params["obfs-password"] = password
					break
				}
			}
		}
		applyFinalMaskParams(finalmask, params)
	}

	var settings map[string]any
	json.Unmarshal([]byte(inbound.Settings), &settings)
	version, _ := settings["version"].(float64)
	protocol := "hysteria2"
	if int(version) == 1 {
		protocol = "hysteria"
	}

	externalProxies, _ := stream["externalProxy"].([]any)
	if len(externalProxies) > 0 {
		links := make([]string, 0, len(externalProxies))
		for _, externalProxy := range externalProxies {
			ep, ok := externalProxy.(map[string]any)
			if !ok {
				continue
			}
			dest, _ := ep["dest"].(string)
			dest = s.resolveExternalAddress(dest)
			portF, okPort := ep["port"].(float64)
			if dest == "" || !okPort {
				continue
			}
			epRemark, _ := ep["remark"].(string)
			link := fmt.Sprintf("%s://%s@%s:%d", protocol, auth, dest, int(portF))
			u, _ := url.Parse(link)
			q := u.Query()
			for k, v := range params {
				q.Add(k, v)
			}
			u.RawQuery = q.Encode()
			u.Fragment = s.genRemark(inbound, email, epRemark)
			links = append(links, u.String())
		}
		return strings.Join(links, "\n")
	}

	link := fmt.Sprintf("%s://%s@%s:%d", protocol, auth, s.resolveAddress(inbound), inbound.Port)
	u, _ := url.Parse(link)
	q := u.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	u.RawQuery = q.Encode()
	u.Fragment = s.genRemark(inbound, email, "")
	return u.String()
}

func (s *SubService) genRemark(inbound *model.Inbound, email string, extra string) string {
	separationChar := string(s.remarkModel[0])
	orderChars := s.remarkModel[1:]
	orders := map[byte]string{
		'i': "",
		'e': "",
		'o': "",
	}
	if len(email) > 0 {
		orders['e'] = email
	}
	if len(inbound.Remark) > 0 {
		orders['i'] = inbound.Remark
	}
	if len(extra) > 0 {
		orders['o'] = extra
	}

	var remark []string
	for i := 0; i < len(orderChars); i++ {
		char := orderChars[i]
		order, exists := orders[char]
		if exists && order != "" {
			remark = append(remark, order)
		}
	}

	if s.showInfo {
		statsExist := false
		var stats xray.ClientTraffic
		for _, clientStat := range inbound.ClientStats {
			if clientStat.Email == email {
				stats = clientStat
				statsExist = true
				break
			}
		}

		if !statsExist && email != "" {
			stats = s.getClientTraffics(inbound.ClientStats, email)
			if stats.Email != "" {
				statsExist = true
			}
		}

		if statsExist && stats.Total == 0 && stats.ExpiryTime == 0 {
			if clients, err := s.inboundService.GetClients(inbound); err == nil {
				for _, c := range clients {
					if c.Email == email {
						tot := c.TotalGB
						if tot > 0 && tot < 1024*1024 {
							tot = tot * 1024 * 1024 * 1024
						}
						stats.Total = tot
						stats.ExpiryTime = c.ExpiryTime
						break
					}
				}
			}
		} else if !statsExist && email != "" {
			if clients, err := s.inboundService.GetClients(inbound); err == nil {
				for _, c := range clients {
					if c.Email == email {
						stats.Email = c.Email
						tot := c.TotalGB
						if tot > 0 && tot < 1024*1024 {
							tot = tot * 1024 * 1024 * 1024
						}
						stats.Total = tot
						stats.ExpiryTime = c.ExpiryTime
						stats.Enable = c.Enable
						statsExist = true
						break
					}
				}
			}
		}

		// Get remained days
		if statsExist {
			if !stats.Enable {
				return fmt.Sprintf("⛔️N/A%s%s", separationChar, strings.Join(remark, separationChar))
			}
			if vol := stats.Total - (stats.Up + stats.Down); vol > 0 {
				remark = append(remark, fmt.Sprintf("%s%s", common.FormatTraffic(vol), "📊"))
			}
			now := time.Now().Unix()
			switch exp := stats.ExpiryTime / 1000; {
			case exp > 0:
				remainingSeconds := exp - now
				days := remainingSeconds / 86400
				hours := (remainingSeconds % 86400) / 3600
				minutes := (remainingSeconds % 3600) / 60
				if days > 0 {
					if hours > 0 {
						remark = append(remark, fmt.Sprintf("%dD,%dH⏳", days, hours))
					} else {
						remark = append(remark, fmt.Sprintf("%dD⏳", days))
					}
				} else if hours > 0 {
					remark = append(remark, fmt.Sprintf("%dH⏳", hours))
				} else {
					remark = append(remark, fmt.Sprintf("%dM⏳", minutes))
				}
			case exp < 0:
				days := exp / -86400
				hours := (exp % -86400) / 3600
				minutes := (exp % -3600) / 60
				if days > 0 {
					if hours > 0 {
						remark = append(remark, fmt.Sprintf("%dD,%dH⏳", days, hours))
					} else {
						remark = append(remark, fmt.Sprintf("%dD⏳", days))
					}
				} else if hours > 0 {
					remark = append(remark, fmt.Sprintf("%dH⏳", hours))
				} else {
					remark = append(remark, fmt.Sprintf("%dM⏳", minutes))
				}
			}
		}
	}
	return strings.Join(remark, separationChar)
}

var validFinalMaskUDPTypes = map[string]struct{}{
	"salamander":       {},
	"mkcp-aes128gcm":   {},
	"header-dns":       {},
	"header-dtls":      {},
	"header-srtp":      {},
	"header-utp":       {},
	"header-wechat":    {},
	"header-wireguard": {},
	"mkcp-original":    {},
	"xdns":             {},
	"xicmp":            {},
	"noise":            {},
	"header-custom":    {},
}

var validFinalMaskTCPTypes = map[string]struct{}{
	"header-custom": {},
	"fragment":      {},
	"sudoku":        {},
}

func applyFinalMaskParams(finalmask map[string]any, params map[string]string) {
	if fm, ok := marshalFinalMask(finalmask); ok {
		params["fm"] = fm
	}
}

func applyFinalMaskObj(finalmask map[string]any, obj map[string]any) {
	if fm, ok := marshalFinalMask(finalmask); ok {
		obj["fm"] = fm
	}
}

func marshalFinalMask(finalmask map[string]any) (string, bool) {
	normalized := normalizeFinalMask(finalmask)
	if !hasFinalMaskContent(normalized) {
		return "", false
	}
	b, err := json.Marshal(normalized)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return "", false
	}
	return string(b), true
}

func normalizeFinalMask(finalmask map[string]any) map[string]any {
	tcpMasks := normalizedFinalMaskTCPMasks(finalmask)
	udpMasks := normalizedFinalMaskUDPMasks(finalmask)
	quicParams, hasQuicParams := finalmask["quicParams"].(map[string]any)

	if len(tcpMasks) == 0 && len(udpMasks) == 0 && !hasQuicParams {
		return nil
	}

	result := map[string]any{}
	if len(tcpMasks) > 0 {
		result["tcp"] = tcpMasks
	}
	if len(udpMasks) > 0 {
		result["udp"] = udpMasks
	}
	if hasQuicParams && len(quicParams) > 0 {
		result["quicParams"] = quicParams
	}
	return result
}

func normalizedFinalMaskTCPMasks(value any) []any {
	finalmask, _ := value.(map[string]any)
	if finalmask == nil {
		return nil
	}
	rawMasks, _ := finalmask["tcp"].([]any)
	if len(rawMasks) == 0 {
		return nil
	}

	normalized := make([]any, 0, len(rawMasks))
	for _, rawMask := range rawMasks {
		mask, _ := rawMask.(map[string]any)
		if mask == nil {
			continue
		}
		maskType, _ := mask["type"].(string)
		if _, ok := validFinalMaskTCPTypes[maskType]; !ok || maskType == "" {
			continue
		}

		normalizedMask := map[string]any{"type": maskType}
		if settings, ok := mask["settings"].(map[string]any); ok && len(settings) > 0 {
			normalizedMask["settings"] = settings
		}
		normalized = append(normalized, normalizedMask)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizedFinalMaskUDPMasks(value any) []any {
	finalmask, _ := value.(map[string]any)
	if finalmask == nil {
		return nil
	}
	rawMasks, _ := finalmask["udp"].([]any)
	if len(rawMasks) == 0 {
		return nil
	}

	normalized := make([]any, 0, len(rawMasks))
	for _, rawMask := range rawMasks {
		mask, _ := rawMask.(map[string]any)
		if mask == nil {
			continue
		}
		maskType, _ := mask["type"].(string)
		if _, ok := validFinalMaskUDPTypes[maskType]; !ok || maskType == "" {
			continue
		}

		normalizedMask := map[string]any{"type": maskType}
		if settings, ok := mask["settings"].(map[string]any); ok && len(settings) > 0 {
			normalizedMask["settings"] = settings
		}
		normalized = append(normalized, normalizedMask)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func hasFinalMaskContent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return len(v) > 0
	case map[string]any:
		for _, item := range v {
			if hasFinalMaskContent(item) {
				return true
			}
		}
		return false
	case []any:
		return slices.ContainsFunc(v, hasFinalMaskContent)
	default:
		return true
	}
}

func searchKey(data any, key string) (any, bool) {
	switch val := data.(type) {
	case map[string]any:
		for k, v := range val {
			if k == key {
				return v, true
			}
			if result, ok := searchKey(v, key); ok {
				return result, true
			}
		}
	case []any:
		for _, v := range val {
			if result, ok := searchKey(v, key); ok {
				return result, true
			}
		}
	}
	return nil, false
}

func searchHost(headers any) string {
	data, _ := headers.(map[string]any)
	for k, v := range data {
		if strings.EqualFold(k, "host") {
			switch v.(type) {
			case []any:
				hosts, _ := v.([]any)
				if len(hosts) > 0 {
					return hosts[0].(string)
				} else {
					return ""
				}
			case any:
				return v.(string)
			}
		}
	}

	return ""
}

// PageData is a view model for subpage.html
// PageData contains data for rendering the subscription information page.
type PageData struct {
	Host         string
	BasePath     string
	SId          string
	Enabled      bool
	Download     string
	Upload       string
	Total        string
	Used         string
	Remained     string
	Expire       int64
	LastOnline   int64
	Datepicker   string
	DownloadByte int64
	UploadByte   int64
	TotalByte    int64
	SubUrl       string
	SubJsonUrl   string
	SubClashUrl  string
	Result       []string
}

// ResolveRequest extracts scheme and host info from request/headers consistently.
// ResolveRequest extracts scheme, host, and header information from an HTTP request.
func (s *SubService) ResolveRequest(c *gin.Context) (scheme string, host string, hostWithPort string, hostHeader string) {
	// scheme
	scheme = "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	// base host (no port)
	if h, err := getHostFromXFH(c.GetHeader("X-Forwarded-Host")); err == nil && h != "" {
		host = h
	}
	if host == "" {
		host = c.GetHeader("X-Real-IP")
	}
	if host == "" {
		var err error
		host, _, err = net.SplitHostPort(c.Request.Host)
		if err != nil {
			host = c.Request.Host
		}
	}

	// host:port for URLs
	hostWithPort = c.GetHeader("X-Forwarded-Host")
	if hostWithPort == "" {
		hostWithPort = c.Request.Host
	}
	if hostWithPort == "" {
		hostWithPort = host
	}

	// header display host
	hostHeader = c.GetHeader("X-Forwarded-Host")
	if hostHeader == "" {
		hostHeader = c.GetHeader("X-Real-IP")
	}
	if hostHeader == "" {
		hostHeader = host
	}
	return
}

// BuildURLs constructs absolute subscription and JSON subscription URLs for a given subscription ID.
// It prioritizes configured URIs, then individual settings, and finally falls back to request-derived components.
func (s *SubService) BuildURLs(scheme, hostWithPort, subPath, subJsonPath, subClashPath, subId string) (subURL, subJsonURL, subClashURL string) {
	if subId == "" {
		return "", "", ""
	}

	configuredSubURI, _ := s.settingService.GetSubURI()
	configuredSubJsonURI, _ := s.settingService.GetSubJsonURI()
	configuredSubClashURI, _ := s.settingService.GetSubClashURI()

	var baseScheme, baseHostWithPort string
	if configuredSubURI == "" || configuredSubJsonURI == "" || configuredSubClashURI == "" {
		baseScheme, baseHostWithPort = s.getBaseSchemeAndHost(scheme, hostWithPort)
	}

	subURL = s.buildSingleURL(configuredSubURI, baseScheme, baseHostWithPort, subPath, subId)
	subJsonURL = s.buildSingleURL(configuredSubJsonURI, baseScheme, baseHostWithPort, subJsonPath, subId)
	subClashURL = s.buildSingleURL(configuredSubClashURI, baseScheme, baseHostWithPort, subClashPath, subId)

	return subURL, subJsonURL, subClashURL
}

// getBaseSchemeAndHost determines the base scheme and host from settings or falls back to request values
func (s *SubService) getBaseSchemeAndHost(requestScheme, requestHostWithPort string) (string, string) {
	subDomain, err := s.settingService.GetSubDomain()
	if err != nil || subDomain == "" {
		return requestScheme, requestHostWithPort
	}

	// Get port and TLS settings
	subPort, _ := s.settingService.GetSubPort()
	subKeyFile, _ := s.settingService.GetSubKeyFile()
	subCertFile, _ := s.settingService.GetSubCertFile()

	// Determine scheme from TLS configuration
	scheme := "http"
	if subKeyFile != "" && subCertFile != "" {
		scheme = "https"
	}

	// Build host:port, always include port for clarity
	hostWithPort := fmt.Sprintf("%s:%d", subDomain, subPort)

	return scheme, hostWithPort
}

// buildSingleURL constructs a single URL using configured URI or base components
func (s *SubService) buildSingleURL(configuredURI, baseScheme, baseHostWithPort, basePath, subId string) string {
	if configuredURI != "" {
		return s.joinPathWithID(configuredURI, subId)
	}

	baseURL := fmt.Sprintf("%s://%s", baseScheme, baseHostWithPort)
	return s.joinPathWithID(baseURL+basePath, subId)
}

// joinPathWithID safely joins a base path with a subscription ID
func (s *SubService) joinPathWithID(basePath, subId string) string {
	if strings.HasSuffix(basePath, "/") {
		return basePath + subId
	}
	return basePath + "/" + subId
}

// BuildPageData parses header and prepares the template view model.
// BuildPageData constructs page data for rendering the subscription information page.
func (s *SubService) BuildPageData(subId string, hostHeader string, traffic xray.ClientTraffic, lastOnline int64, subs []string, subURL, subJsonURL, subClashURL string, basePath string) PageData {
	download := common.FormatTraffic(traffic.Down)
	upload := common.FormatTraffic(traffic.Up)
	total := "∞"
	used := common.FormatTraffic(traffic.Up + traffic.Down)
	remained := ""
	if traffic.Total > 0 {
		total = common.FormatTraffic(traffic.Total)
		left := max(traffic.Total-(traffic.Up+traffic.Down), 0)
		remained = common.FormatTraffic(left)
	}

	datepicker := s.datepicker
	if datepicker == "" {
		datepicker = "gregorian"
	}

	return PageData{
		Host:         hostHeader,
		BasePath:     basePath,
		SId:          subId,
		Enabled:      traffic.Enable,
		Download:     download,
		Upload:       upload,
		Total:        total,
		Used:         used,
		Remained:     remained,
		Expire:       traffic.ExpiryTime / 1000,
		LastOnline:   lastOnline,
		Datepicker:   datepicker,
		DownloadByte: traffic.Down,
		UploadByte:   traffic.Up,
		TotalByte:    traffic.Total,
		SubUrl:       subURL,
		SubJsonUrl:   subJsonURL,
		SubClashUrl:  subClashURL,
		Result:       subs,
	}
}

func getHostFromXFH(s string) (string, error) {
	if strings.Contains(s, ":") {
		realHost, _, err := net.SplitHostPort(s)
		if err != nil {
			return "", err
		}
		return realHost, nil
	}
	return s, nil
}
