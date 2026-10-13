package service

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/util/random"
	"github.com/mhsanaei/3x-ui/v2/xray"
	"gorm.io/gorm"
)

type InboundBrief struct {
	Id       int            `json:"id"`
	Tag      string         `json:"tag"`
	Protocol model.Protocol `json:"protocol"`
	Port     int            `json:"port"`
	Remark   string         `json:"remark"`
	Listen   string         `json:"listen"`
}

type ClientDetail struct {
	Id         string         `json:"id"`
	Password   string         `json:"password"`
	Auth       string         `json:"auth"`
	Flow       string         `json:"flow"`
	Email      string         `json:"email"`
	SubId      string         `json:"subId"`
	Total      int64          `json:"total"`
	TotalGB    int64          `json:"totalGB"`
	ExpiryTime int64          `json:"expiryTime"`
	Enable     bool           `json:"enable"`
	TgId       int64          `json:"tgId"`
	Comment    string         `json:"comment"`
	Reset      int            `json:"reset"`
	LimitIp    int            `json:"limitIp"`
	CreatedAt  int64          `json:"created_at"`
	UpdatedAt  int64          `json:"updated_at"`
	InboundIds []int          `json:"inboundIds"`
	Inbounds   []InboundBrief `json:"inbounds"`
	Up         int64          `json:"up"`
	Down       int64          `json:"down"`
	AllTime    int64          `json:"allTime"`
	LastOnline int64          `json:"lastOnline"`
	Online     bool           `json:"online"`
}

type ClientSummary struct {
	TotalCount    int   `json:"totalCount"`
	ActiveCount   int   `json:"activeCount"`
	DepletedCount int   `json:"depletedCount"`
	ExpiringCount int   `json:"expiringCount"`
	DisabledCount int   `json:"disabledCount"`
	OnlineCount   int   `json:"onlineCount"`
	TotalUp       int64 `json:"totalUp"`
	TotalDown     int64 `json:"totalDown"`
}

type ClientService struct {
	inboundService InboundService
	settingService SettingService
	xrayApi        xray.XrayAPI
}

func (s *ClientService) GetInboundOptions() ([]InboundBrief, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(&model.Inbound{}).
		Where("protocol IN ('vmess','vless','trojan','shadowsocks','hysteria','hysteria2')").
		Order("id DESC").
		Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	result := make([]InboundBrief, 0, len(inbounds))
	for _, in := range inbounds {
		result = append(result, InboundBrief{
			Id:       in.Id,
			Tag:      in.Tag,
			Protocol: in.Protocol,
			Port:     in.Port,
			Remark:   in.Remark,
			Listen:   in.Listen,
		})
	}
	return result, nil
}

func (s *ClientService) GetClientsList() (*ClientSummary, []*ClientDetail, error) {
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, nil, err
	}

	db := database.GetDB()
	var traffics []xray.ClientTraffic
	_ = db.Model(&xray.ClientTraffic{}).Find(&traffics).Error
	trafficMap := make(map[string]xray.ClientTraffic, len(traffics))
	for _, t := range traffics {
		trafficMap[strings.ToLower(t.Email)] = t
	}

	onlineList := s.inboundService.GetOnlineClients()
	onlineMap := make(map[string]bool, len(onlineList))
	for _, o := range onlineList {
		onlineMap[strings.ToLower(o)] = true
	}

	var ips []model.InboundClientIps
	_ = db.Model(&model.InboundClientIps{}).Find(&ips).Error
	ipMap := make(map[string]string, len(ips))
	for _, ip := range ips {
		ipMap[strings.ToLower(ip.ClientEmail)] = ip.Ips
	}

	clientMap := make(map[string]*ClientDetail)

	for _, inbound := range inbounds {
		clients, err := s.inboundService.GetClients(inbound)
		if err != nil || len(clients) == 0 {
			continue
		}

		inboundBrief := InboundBrief{
			Id:       inbound.Id,
			Tag:      inbound.Tag,
			Protocol: inbound.Protocol,
			Port:     inbound.Port,
			Remark:   inbound.Remark,
			Listen:   inbound.Listen,
		}

		for _, client := range clients {
			if strings.TrimSpace(client.Email) == "" {
				continue
			}
			key := strings.ToLower(client.Email)
			detail, exists := clientMap[key]
			if !exists {
				detail = &ClientDetail{
					Email:      client.Email,
					SubId:      client.SubID,
					Id:         client.ID,
					Password:   client.Password,
					Auth:       client.Auth,
					Flow:       client.Flow,
					TotalGB:    client.TotalGB,
					ExpiryTime: client.ExpiryTime,
					Enable:     client.Enable,
					TgId:       client.TgID,
					Comment:    client.Comment,
					Reset:      client.Reset,
					LimitIp:    client.LimitIP,
					CreatedAt:  client.CreatedAt,
					UpdatedAt:  client.UpdatedAt,
					InboundIds: []int{inbound.Id},
					Inbounds:   []InboundBrief{inboundBrief},
				}

				if ct, ok := trafficMap[key]; ok {
					detail.Up = ct.Up
					detail.Down = ct.Down
					detail.AllTime = ct.AllTime
					detail.LastOnline = ct.LastOnline
					detail.Total = ct.Total
					if ct.Total > 0 {
						detail.TotalGB = ct.Total / (1024 * 1024 * 1024)
					}
					if ct.ExpiryTime != 0 {
						detail.ExpiryTime = ct.ExpiryTime
					}
					detail.Enable = ct.Enable
				} else {
					detail.Total = client.TotalGB
					if client.TotalGB > 0 {
						detail.TotalGB = client.TotalGB / (1024 * 1024 * 1024)
					}
				}

				detail.Online = onlineMap[key]
				clientMap[key] = detail
			} else {
				detail.InboundIds = append(detail.InboundIds, inbound.Id)
				detail.Inbounds = append(detail.Inbounds, inboundBrief)

				if detail.SubId == "" && client.SubID != "" {
					detail.SubId = client.SubID
				}
				if detail.Id == "" && client.ID != "" {
					detail.Id = client.ID
				}
				if detail.Password == "" && client.Password != "" {
					detail.Password = client.Password
				}
				if detail.Auth == "" && client.Auth != "" {
					detail.Auth = client.Auth
				}
				if detail.Comment == "" && client.Comment != "" {
					detail.Comment = client.Comment
				}
				if detail.LimitIp == 0 && client.LimitIP > 0 {
					detail.LimitIp = client.LimitIP
				}
			}
		}
	}

	// Also check for any clients that might exist only in client_traffics (orphans)
	for _, ct := range traffics {
		key := strings.ToLower(ct.Email)
		if _, exists := clientMap[key]; !exists && ct.Email != "" {
			clientMap[key] = &ClientDetail{
				Email:      ct.Email,
				SubId:      ct.SubId,
				Id:         ct.UUID,
				TotalGB:    ct.Total,
				ExpiryTime: ct.ExpiryTime,
				Enable:     ct.Enable,
				Reset:      ct.Reset,
				Up:         ct.Up,
				Down:       ct.Down,
				AllTime:    ct.AllTime,
				LastOnline: ct.LastOnline,
				Online:     onlineMap[key],
				InboundIds: []int{},
				Inbounds:   []InboundBrief{},
			}
		}
	}

	now := time.Now().Unix() * 1000
	threeDaysMs := int64(3 * 24 * 3600 * 1000)
	summary := &ClientSummary{}

	clientsList := make([]*ClientDetail, 0, len(clientMap))
	for _, c := range clientMap {
		summary.TotalCount++
		summary.TotalUp += c.Up
		summary.TotalDown += c.Down

		isDepleted := c.Total > 0 && (c.Up+c.Down >= c.Total)
		isExpired := c.ExpiryTime > 0 && c.ExpiryTime <= now

		if !c.Enable {
			summary.DisabledCount++
		} else if isDepleted || isExpired {
			summary.DepletedCount++
		} else {
			summary.ActiveCount++
		}

		if c.ExpiryTime > now && c.ExpiryTime-now <= threeDaysMs {
			summary.ExpiringCount++
		}

		if c.Online {
			summary.OnlineCount++
		}

		clientsList = append(clientsList, c)
	}

	sort.Slice(clientsList, func(i, j int) bool {
		if clientsList[i].UpdatedAt != clientsList[j].UpdatedAt {
			return clientsList[i].UpdatedAt > clientsList[j].UpdatedAt
		}
		return clientsList[i].Email < clientsList[j].Email
	})

	return summary, clientsList, nil
}

func (s *ClientService) AddClient(client *model.Client, inboundIds []int) (bool, error) {
	if client == nil {
		return false, common.NewError("client data is required")
	}
	email := strings.TrimSpace(client.Email)
	if email == "" {
		return false, common.NewError("client email is required")
	}
	if len(inboundIds) == 0 {
		return false, common.NewError("at least one inbound is required")
	}

	// Check if email already exists in any inbound
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return false, err
	}
	for _, in := range inbounds {
		existingClients, _ := s.inboundService.GetClients(in)
		for _, ec := range existingClients {
			if strings.EqualFold(ec.Email, email) {
				return false, common.NewError("client email already exists: ", email)
			}
		}
	}

	if client.SubID == "" {
		client.SubID = client.Email
	}
	if client.SubID == "" {
		client.SubID = random.Seq(16)
	}
	if client.ID == "" {
		client.ID = uuid.NewString()
	}
	if client.Password == "" {
		client.Password = random.Seq(10)
	}
	if client.Auth == "" {
		client.Auth = random.Seq(10)
	}

	if client.TotalGB > 0 && client.TotalGB < 1024*1024 {
		client.TotalGB = client.TotalGB * 1024 * 1024 * 1024
	}

	nowTs := time.Now().Unix() * 1000
	if client.CreatedAt == 0 {
		client.CreatedAt = nowTs
	}
	client.UpdatedAt = nowTs

	needRestart := false
	db := database.GetDB()

	var apiPort int
	if p != nil {
		apiPort = p.GetAPIPort()
	}

	for _, inId := range inboundIds {
		inbound, err := s.inboundService.GetInbound(inId)
		if err != nil {
			logger.Warningf("AddClient: inbound %d not found, skipping", inId)
			continue
		}

		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			logger.Errorf("AddClient: failed to unmarshal inbound %d settings: %v", inId, err)
			continue
		}

		clientsArr, _ := settings["clients"].([]any)
		if clientsArr == nil {
			clientsArr = make([]any, 0)
		}

		clientCopy := *client
		switch inbound.Protocol {
		case model.VMESS, model.VLESS:
			if clientCopy.ID == "" {
				clientCopy.ID = uuid.NewString()
			}
		case model.Trojan, model.Shadowsocks:
			if clientCopy.Password == "" {
				clientCopy.Password = random.Seq(10)
			}
		case model.Hysteria, model.Hysteria2:
			if clientCopy.Auth == "" {
				clientCopy.Auth = random.Seq(10)
			}
		}

		clientBytes, _ := json.Marshal(clientCopy)
		var clientMap map[string]any
		_ = json.Unmarshal(clientBytes, &clientMap)

		clientsArr = append(clientsArr, clientMap)
		settings["clients"] = clientsArr

		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			logger.Errorf("AddClient: failed to marshal inbound %d settings: %v", inId, err)
			continue
		}

		inbound.Settings = string(newSettings)
		if err := db.Save(inbound).Error; err != nil {
			logger.Errorf("AddClient: failed to save inbound %d: %v", inId, err)
			continue
		}

		// Add user via Xray API if enabled
		if client.Enable && inbound.Enable {
			if apiPort > 0 {
				s.xrayApi.Init(apiPort)
				cipher := ""
				if inbound.Protocol == model.Shadowsocks {
					if m, ok := settings["method"].(string); ok {
						cipher = m
					}
				}
				err1 := s.xrayApi.AddUser(string(inbound.Protocol), inbound.Tag, map[string]any{
					"email":    clientCopy.Email,
					"id":       clientCopy.ID,
					"auth":     clientCopy.Auth,
					"security": clientCopy.Security,
					"flow":     clientCopy.Flow,
					"password": clientCopy.Password,
					"cipher":   cipher,
				})
				if err1 != nil {
					logger.Debug("AddUser via API error:", err1)
					needRestart = true
				}
				s.xrayApi.Close()
			} else {
				needRestart = true
			}
		}
	}

	// Update / create client traffic entry
	var ct xray.ClientTraffic
	err = db.Where("email = ?", client.Email).First(&ct).Error
	if err != nil && err == gorm.ErrRecordNotFound {
		ct = xray.ClientTraffic{
			InboundId:  inboundIds[0],
			Email:      client.Email,
			Total:      client.TotalGB,
			ExpiryTime: client.ExpiryTime,
			Enable:     client.Enable,
			Reset:      client.Reset,
			Up:         0,
			Down:       0,
			AllTime:    0,
		}
		_ = db.Create(&ct).Error
	} else if err == nil {
		ct.Total = client.TotalGB
		ct.ExpiryTime = client.ExpiryTime
		ct.Enable = client.Enable
		ct.Reset = client.Reset
		_ = db.Save(&ct).Error
	}

	// Handle IP limits
	if client.LimitIP > 0 {
		var clientIps model.InboundClientIps
		if err := db.Where("client_email = ?", client.Email).First(&clientIps).Error; err == gorm.ErrRecordNotFound {
			clientIps = model.InboundClientIps{
				ClientEmail: client.Email,
				Ips:         "",
			}
			_ = db.Create(&clientIps).Error
		}
	}

	return needRestart, nil
}

func (s *ClientService) UpdateClient(oldEmail string, client *model.Client, targetInboundIds []int) (bool, error) {
	if strings.TrimSpace(oldEmail) == "" {
		return false, common.NewError("old email is required")
	}
	if client == nil {
		return false, common.NewError("client data is required")
	}
	newEmail := strings.TrimSpace(client.Email)
	if newEmail == "" {
		newEmail = oldEmail
		client.Email = oldEmail
	}
	if client.SubID == "" {
		client.SubID = newEmail
	}

	if client.TotalGB > 0 && client.TotalGB < 1024*1024 {
		client.TotalGB = client.TotalGB * 1024 * 1024 * 1024
	}

	db := database.GetDB()
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return false, err
	}

	nowTs := time.Now().Unix() * 1000
	client.UpdatedAt = nowTs

	var apiPort int
	if p != nil {
		apiPort = p.GetAPIPort()
	}
	needRestart := false

	targetSet := make(map[int]bool, len(targetInboundIds))
	for _, id := range targetInboundIds {
		targetSet[id] = true
	}

	for _, inbound := range inbounds {
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clientsArr, _ := settings["clients"].([]any)
		if clientsArr == nil {
			continue
		}

		containsOld := false
		clientIndex := -1
		var existingClientMap map[string]any

		for idx, item := range clientsArr {
			if cm, ok := item.(map[string]any); ok {
				if e, ok2 := cm["email"].(string); ok2 && strings.EqualFold(e, oldEmail) {
					containsOld = true
					clientIndex = idx
					existingClientMap = cm
					break
				}
			}
		}

		inTarget := targetSet[inbound.Id]

		if containsOld && !inTarget {
			// REMOVE client from this inbound
			newClients := make([]any, 0, len(clientsArr)-1)
			for idx, item := range clientsArr {
				if idx != clientIndex {
					newClients = append(newClients, item)
				}
			}
			settings["clients"] = newClients
			newSettings, _ := json.MarshalIndent(settings, "", "  ")
			inbound.Settings = string(newSettings)
			_ = db.Save(inbound).Error

			if apiPort > 0 {
				s.xrayApi.Init(apiPort)
				_ = s.xrayApi.RemoveUser(inbound.Tag, oldEmail)
				s.xrayApi.Close()
			}
		} else if containsOld && inTarget {
			// UPDATE client in this inbound
			clientCopy := *client
			if clientCopy.ID == "" {
				if id, ok := existingClientMap["id"].(string); ok {
					clientCopy.ID = id
				}
			}
			if clientCopy.Password == "" {
				if pwd, ok := existingClientMap["password"].(string); ok {
					clientCopy.Password = pwd
				}
			}
			if clientCopy.Auth == "" {
				if auth, ok := existingClientMap["auth"].(string); ok {
					clientCopy.Auth = auth
				}
			}
			if ca, ok := existingClientMap["created_at"].(float64); ok && clientCopy.CreatedAt == 0 {
				clientCopy.CreatedAt = int64(ca)
			}

			clientBytes, _ := json.Marshal(clientCopy)
			var updatedMap map[string]any
			_ = json.Unmarshal(clientBytes, &updatedMap)

			clientsArr[clientIndex] = updatedMap
			settings["clients"] = clientsArr

			newSettings, _ := json.MarshalIndent(settings, "", "  ")
			inbound.Settings = string(newSettings)
			_ = db.Save(inbound).Error

			if apiPort > 0 {
				s.xrayApi.Init(apiPort)
				_ = s.xrayApi.RemoveUser(inbound.Tag, oldEmail)
				if clientCopy.Enable && inbound.Enable {
					cipher := ""
					if inbound.Protocol == model.Shadowsocks {
						if m, ok := settings["method"].(string); ok {
							cipher = m
						}
					}
					err1 := s.xrayApi.AddUser(string(inbound.Protocol), inbound.Tag, map[string]any{
						"email":    clientCopy.Email,
						"id":       clientCopy.ID,
						"auth":     clientCopy.Auth,
						"security": clientCopy.Security,
						"flow":     clientCopy.Flow,
						"password": clientCopy.Password,
						"cipher":   cipher,
					})
					if err1 != nil {
						logger.Debug("AddUser via API error:", err1)
						needRestart = true
					}
				}
				s.xrayApi.Close()
			} else if clientCopy.Enable && inbound.Enable {
				needRestart = true
			}
		} else if !containsOld && inTarget {
			// ADD client to this inbound
			clientCopy := *client
			switch inbound.Protocol {
			case model.VMESS, model.VLESS:
				if clientCopy.ID == "" {
					clientCopy.ID = uuid.NewString()
				}
			case model.Trojan, model.Shadowsocks:
				if clientCopy.Password == "" {
					clientCopy.Password = random.Seq(10)
				}
			case model.Hysteria, model.Hysteria2:
				if clientCopy.Auth == "" {
					clientCopy.Auth = random.Seq(10)
				}
			}

			clientBytes, _ := json.Marshal(clientCopy)
			var newMap map[string]any
			_ = json.Unmarshal(clientBytes, &newMap)

			clientsArr = append(clientsArr, newMap)
			settings["clients"] = clientsArr

			newSettings, _ := json.MarshalIndent(settings, "", "  ")
			inbound.Settings = string(newSettings)
			_ = db.Save(inbound).Error

			if clientCopy.Enable && inbound.Enable {
				if apiPort > 0 {
					s.xrayApi.Init(apiPort)
					cipher := ""
					if inbound.Protocol == model.Shadowsocks {
						if m, ok := settings["method"].(string); ok {
							cipher = m
						}
					}
					err1 := s.xrayApi.AddUser(string(inbound.Protocol), inbound.Tag, map[string]any{
						"email":    clientCopy.Email,
						"id":       clientCopy.ID,
						"auth":     clientCopy.Auth,
						"security": clientCopy.Security,
						"flow":     clientCopy.Flow,
						"password": clientCopy.Password,
						"cipher":   cipher,
					})
					if err1 != nil {
						logger.Debug("AddUser via API error:", err1)
						needRestart = true
					}
					s.xrayApi.Close()
				} else {
					needRestart = true
				}
			}
		}
	}

	// Update client_traffics
	var ct xray.ClientTraffic
	err = db.Where("email = ?", oldEmail).First(&ct).Error
	if err == nil {
		ct.Email = newEmail
		ct.Total = client.TotalGB
		ct.ExpiryTime = client.ExpiryTime
		ct.Enable = client.Enable
		ct.Reset = client.Reset
		if len(targetInboundIds) > 0 {
			ct.InboundId = targetInboundIds[0]
		}
		_ = db.Save(&ct).Error
	} else if err == gorm.ErrRecordNotFound && len(targetInboundIds) > 0 {
		ct = xray.ClientTraffic{
			InboundId:  targetInboundIds[0],
			Email:      newEmail,
			Total:      client.TotalGB,
			ExpiryTime: client.ExpiryTime,
			Enable:     client.Enable,
			Reset:      client.Reset,
		}
		_ = db.Create(&ct).Error
	}

	// Update inbound_client_ips if renamed
	if !strings.EqualFold(oldEmail, newEmail) {
		db.Model(&model.InboundClientIps{}).Where("client_email = ?", oldEmail).Update("client_email", newEmail)
	}

	return needRestart, nil
}

func (s *ClientService) DeleteClient(email string) (bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, common.NewError("email is required")
	}

	db := database.GetDB()
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return false, err
	}

	var apiPort int
	if p != nil {
		apiPort = p.GetAPIPort()
	}
	needRestart := false

	for _, inbound := range inbounds {
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clientsArr, _ := settings["clients"].([]any)
		if clientsArr == nil {
			continue
		}

		found := false
		newClients := make([]any, 0, len(clientsArr))
		for _, item := range clientsArr {
			if cm, ok := item.(map[string]any); ok {
				if e, ok2 := cm["email"].(string); ok2 && strings.EqualFold(e, email) {
					found = true
					continue
				}
			}
			newClients = append(newClients, item)
		}

		if found {
			settings["clients"] = newClients
			newSettings, _ := json.MarshalIndent(settings, "", "  ")
			inbound.Settings = string(newSettings)
			_ = db.Save(inbound).Error

			if apiPort > 0 {
				s.xrayApi.Init(apiPort)
				err1 := s.xrayApi.RemoveUser(inbound.Tag, email)
				if err1 != nil && !strings.Contains(err1.Error(), "not found") {
					logger.Debug("RemoveUser via API error:", err1)
					needRestart = true
				}
				s.xrayApi.Close()
			} else {
				needRestart = true
			}
		}
	}

	_ = db.Where("email = ?", email).Delete(&xray.ClientTraffic{}).Error
	_ = db.Where("client_email = ?", email).Delete(&model.InboundClientIps{}).Error

	return needRestart, nil
}

func (s *ClientService) ToggleEnableClient(email string, enable bool) (bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, common.NewError("email is required")
	}

	db := database.GetDB()
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return false, err
	}

	var apiPort int
	if p != nil {
		apiPort = p.GetAPIPort()
	}
	needRestart := false

	for _, inbound := range inbounds {
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clientsArr, _ := settings["clients"].([]any)
		if clientsArr == nil {
			continue
		}

		found := false
		var targetClientMap map[string]any

		for _, item := range clientsArr {
			if cm, ok := item.(map[string]any); ok {
				if e, ok2 := cm["email"].(string); ok2 && strings.EqualFold(e, email) {
					cm["enable"] = enable
					found = true
					targetClientMap = cm
					break
				}
			}
		}

		if found {
			newSettings, _ := json.MarshalIndent(settings, "", "  ")
			inbound.Settings = string(newSettings)
			_ = db.Save(inbound).Error

			if apiPort > 0 {
				s.xrayApi.Init(apiPort)
				if enable && inbound.Enable {
					cipher := ""
					if inbound.Protocol == model.Shadowsocks {
						if m, ok := settings["method"].(string); ok {
							cipher = m
						}
					}
					id, _ := targetClientMap["id"].(string)
					auth, _ := targetClientMap["auth"].(string)
					sec, _ := targetClientMap["security"].(string)
					flow, _ := targetClientMap["flow"].(string)
					pwd, _ := targetClientMap["password"].(string)

					err1 := s.xrayApi.AddUser(string(inbound.Protocol), inbound.Tag, map[string]any{
						"email":    email,
						"id":       id,
						"auth":     auth,
						"security": sec,
						"flow":     flow,
						"password": pwd,
						"cipher":   cipher,
					})
					if err1 != nil {
						logger.Debug("AddUser via API error:", err1)
						needRestart = true
					}
				} else {
					err1 := s.xrayApi.RemoveUser(inbound.Tag, email)
					if err1 != nil && !strings.Contains(err1.Error(), "not found") {
						logger.Debug("RemoveUser via API error:", err1)
						needRestart = true
					}
				}
				s.xrayApi.Close()
			} else {
				needRestart = true
			}
		}
	}

	_ = db.Model(&xray.ClientTraffic{}).Where("email = ?", email).Update("enable", enable).Error

	return needRestart, nil
}

func (s *ClientService) ResetClientTraffic(email string) (bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, common.NewError("email is required")
	}

	db := database.GetDB()
	err := db.Model(&xray.ClientTraffic{}).
		Where("email = ?", email).
		Updates(map[string]any{
			"up":   0,
			"down": 0,
		}).Error
	if err != nil {
		return false, err
	}

	return false, nil
}
