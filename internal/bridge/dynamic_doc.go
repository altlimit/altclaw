package bridge

import (
	"fmt"
	"reflect"
	"strings"
)

// BuildDynamicDocFunc creates a DynamicDocFunc that generates documentation
// from live config structs. Supports "settings" (workspace), "config" (app config),
// and "providers" (list of configured providers).
func BuildDynamicDocFunc(ctx ConfirmContext) DynamicDocFunc {
	return func(name string) string {
		switch name {
		case "settings":
			return buildSettingsDoc(ctx)
		case "config":
			return buildConfigDoc(ctx)
		case "providers":
			return buildProvidersDoc(ctx)
		default:
			return ""
		}
	}
}

func buildSettingsDoc(ctx ConfirmContext) string {
	ws := ctx.Workspace()
	if ws == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### [ settings ] — Workspace Settings\n\n")
	sb.WriteString("Modifiable via ui.confirm(\"settings.update\", { scope: \"workspace\", field: value, ... })\n\n")
	sb.WriteString("| Field | Type | Current Value | Description |\n")
	sb.WriteString("|---|---|---|---|\n")
	writeStructFields(&sb, reflect.ValueOf(*ws), settingsSkip)
	return sb.String()
}

func buildConfigDoc(ctx ConfirmContext) string {
	store := ctx.Store()
	if store == nil {
		return ""
	}
	appCfg := store.Config()
	if appCfg == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### [ config ] — App Configuration\n\n")
	sb.WriteString("Modifiable via ui.confirm(\"settings.update\", { scope: \"user\", field: value, ... })\n\n")
	sb.WriteString("| Field | Type | Current Value | Description |\n")
	sb.WriteString("|---|---|---|---|\n")
	writeStructFields(&sb, reflect.ValueOf(*appCfg), configSkip)
	return sb.String()
}

func buildProvidersDoc(ctx ConfirmContext) string {
	store := ctx.Store()
	if store == nil {
		return ""
	}
	providers, err := store.ListProviders()
	if err != nil || len(providers) == 0 {
		return "No providers configured."
	}
	var sb strings.Builder
	sb.WriteString("### [ providers ] — Configured AI Providers\n\n")
	sb.WriteString("Manage via ui.confirm(\"provider.add|update|delete\", params)\n\n")
	for _, p := range providers {
		sb.WriteString(fmt.Sprintf("- **%s** — %s/%s", p.Name, p.ProviderType, p.Model))
		if p.Description != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", p.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nProvider fields: name, provider, model, api_key, base_url, host, description, docker_image, rate_limit, daily_prompt_cap, daily_completion_cap, docs\n")
	return sb.String()
}

// settingsSkip are Workspace fields to omit from docs (internal/sensitive).
var settingsSkip = map[string]bool{
	"Base": true, "ID": true, "Path": true, "Name": true,
	"TunnelToken": true, "TunnelHub": true, "TunnelAddr": true,
	"OpenTabs": true, "Port": true, "LastActive": true,
	"CreatedAt": true, "LastProvider": true,
}

// configSkip are AppConfig fields to omit from docs (internal/sensitive).
var configSkip = map[string]bool{
	"Base": true, "ID": true,
	"VAPIDPublicKey": true, "VAPIDPrivateKey": true,
	"ModulePublicKey": true, "ModulePrivateKey": true,
	"SecretPublicKey": true, "SecretPrivateKey": true,
}

// writeStructFields writes a markdown table row for each exported field.
func writeStructFields(sb *strings.Builder, v reflect.Value, skip map[string]bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || skip[f.Name] {
			continue
		}
		// Use json tag name if available
		jsonName := f.Name
		if tag := f.Tag.Get("json"); tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] != "" && parts[0] != "-" {
				jsonName = parts[0]
			} else if parts[0] == "-" {
				continue // skip json:"-" fields
			}
		}
		fv := v.Field(i)
		typeName := friendlyType(f.Type)
		val := formatValue(fv, jsonName)
		help := f.Tag.Get("help")
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", jsonName, typeName, val, help))
	}
}

func friendlyType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice:
		return "[]" + friendlyType(t.Elem())
	default:
		return t.String()
	}
}

func formatValue(v reflect.Value, name string) string {
	// Mask sensitive fields
	nameLow := strings.ToLower(name)
	if strings.Contains(nameLow, "key") || strings.Contains(nameLow, "token") ||
		strings.Contains(nameLow, "secret") || strings.Contains(nameLow, "password") {
		s := v.String()
		if len(s) > 8 {
			return s[:4] + "..." + s[len(s)-4:]
		}
		if s != "" {
			return "***"
		}
		return "(empty)"
	}

	if v.IsZero() {
		return "(default)"
	}
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if len(s) > 60 {
			return s[:60] + "..."
		}
		return s
	case reflect.Slice:
		if v.Len() == 0 {
			return "(empty)"
		}
		var items []string
		for i := 0; i < v.Len() && i < 5; i++ {
			items = append(items, fmt.Sprintf("%v", v.Index(i).Interface()))
		}
		if v.Len() > 5 {
			items = append(items, fmt.Sprintf("...+%d more", v.Len()-5))
		}
		return strings.Join(items, ", ")
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
