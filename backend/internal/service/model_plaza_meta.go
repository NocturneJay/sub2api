package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ModelPlazaModelMeta 模型广场里单个模型的展示元数据。
// 仅影响用户端「模型广场」页面的展示,不参与计费、路由或模型可用性判断。
type ModelPlazaModelMeta struct {
	// Endpoints 该模型对用户展示的可用端点列表,如 "/v1/chat/completions"。
	Endpoints []string `json:"endpoints"`
}

// ModelPlazaMeta 模型广场展示元数据:模型名 → 元数据。
type ModelPlazaMeta struct {
	Models map[string]ModelPlazaModelMeta `json:"models"`
}

// 防御性上限:管理端误操作(粘贴超大 JSON)时保护 settings 表。
const (
	modelPlazaMetaMaxModels    = 1000
	modelPlazaMetaMaxEndpoints = 20
	modelPlazaMetaMaxStrLen    = 200
)

func emptyModelPlazaMeta() *ModelPlazaMeta {
	return &ModelPlazaMeta{Models: map[string]ModelPlazaModelMeta{}}
}

// GetModelPlazaMeta 读取模型广场展示元数据。未配置或数据损坏时返回空集,
// 保证用户页面始终可渲染。
func (s *SettingService) GetModelPlazaMeta(ctx context.Context) (*ModelPlazaMeta, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyModelPlazaMeta)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return emptyModelPlazaMeta(), nil
		}
		return nil, fmt.Errorf("get model plaza meta: %w", err)
	}
	if value == "" {
		return emptyModelPlazaMeta(), nil
	}

	var meta ModelPlazaMeta
	if err := json.Unmarshal([]byte(value), &meta); err != nil {
		return emptyModelPlazaMeta(), nil
	}
	if meta.Models == nil {
		meta.Models = map[string]ModelPlazaModelMeta{}
	}
	return &meta, nil
}

// SetModelPlazaMeta 保存模型广场展示元数据。写入前做归一化:
// 修剪空白、丢弃空模型名/空端点、超限截断;端点列表为空的条目不落库。
func (s *SettingService) SetModelPlazaMeta(ctx context.Context, meta *ModelPlazaMeta) error {
	if meta == nil {
		return fmt.Errorf("meta cannot be nil")
	}
	if len(meta.Models) > modelPlazaMetaMaxModels {
		return fmt.Errorf("too many models: %d (max %d)", len(meta.Models), modelPlazaMetaMaxModels)
	}

	normalized := make(map[string]ModelPlazaModelMeta, len(meta.Models))
	for name, m := range meta.Models {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > modelPlazaMetaMaxStrLen {
			continue
		}
		endpoints := make([]string, 0, len(m.Endpoints))
		for _, ep := range m.Endpoints {
			ep = strings.TrimSpace(ep)
			if ep == "" || len(ep) > modelPlazaMetaMaxStrLen {
				continue
			}
			endpoints = append(endpoints, ep)
			if len(endpoints) >= modelPlazaMetaMaxEndpoints {
				break
			}
		}
		if len(endpoints) == 0 {
			continue
		}
		normalized[name] = ModelPlazaModelMeta{Endpoints: endpoints}
	}

	data, err := json.Marshal(&ModelPlazaMeta{Models: normalized})
	if err != nil {
		return fmt.Errorf("marshal model plaza meta: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyModelPlazaMeta, string(data))
}
