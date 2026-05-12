package server

import (
	"fmt"
	"strings"
)

// ToolRegistry holds metadata about available MCP tools
type ToolRegistry struct {
	tools map[string]*ToolInfo
}

// ToolInfo contains metadata about an MCP tool
type ToolInfo struct {
	Name         string
	Description  string
	Category     string // "read", "write", "delete", "sensitive"
	DefaultAllow bool   // whether tool is allowed by default
}

// NewToolRegistry creates a new tool registry with all available tools
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: map[string]*ToolInfo{
			"list_teams": {
				Name:         "list_teams",
				Description:  "List teams available to the current actor.",
				Category:     "read",
				DefaultAllow: true,
			},
			"list_apps": {
				Name:         "list_apps",
				Description:  "List apps in a team.",
				Category:     "read",
				DefaultAllow: true,
			},
			"get_app": {
				Name:         "get_app",
				Description:  "Get an app in a team.",
				Category:     "read",
				DefaultAllow: true,
			},
			"create_app_preview": {
				Name:         "create_app_preview",
				Description:  "Create preview for create_app action.",
				Category:     "write",
				DefaultAllow: false,
			},
			"create_app": {
				Name:         "create_app",
				Description:  "Confirm create_app with preview_id.",
				Category:     "write",
				DefaultAllow: false,
			},
			"list_members": {
				Name:         "list_members",
				Description:  "List members in a team.",
				Category:     "read",
				DefaultAllow: true,
			},
			"invite_member_preview": {
				Name:         "invite_member_preview",
				Description:  "Create preview for inviting a team member.",
				Category:     "write",
				DefaultAllow: false,
			},
			"invite_member": {
				Name:         "invite_member",
				Description:  "Confirm member invite using preview_id.",
				Category:     "write",
				DefaultAllow: false,
			},
			"remove_member_preview": {
				Name:         "remove_member_preview",
				Description:  "Create preview for removing a member.",
				Category:     "delete",
				DefaultAllow: false,
			},
			"remove_member": {
				Name:         "remove_member",
				Description:  "Confirm member removal using preview_id.",
				Category:     "delete",
				DefaultAllow: false,
			},
		},
	}
}

// GetTool returns tool info by name
func (r *ToolRegistry) GetTool(name string) (*ToolInfo, error) {
	if tool, ok := r.tools[name]; ok {
		return tool, nil
	}
	return nil, fmt.Errorf("tool not found: %s", name)
}

// GetAllTools returns all available tools
func (r *ToolRegistry) GetAllTools() []*ToolInfo {
	tools := make([]*ToolInfo, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetToolsByCategory returns all tools in a specific category
func (r *ToolRegistry) GetToolsByCategory(category string) []*ToolInfo {
	var tools []*ToolInfo
	for _, tool := range r.tools {
		if tool.Category == category {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetToolsForUser returns the list of tools available to a user based on granted tools
// If grantedTools is nil or empty, returns only default-allow tools
func (r *ToolRegistry) GetToolsForUser(grantedTools []string) []*ToolInfo {
	if len(grantedTools) == 0 {
		// Return only default-allow tools
		var tools []*ToolInfo
		for _, tool := range r.tools {
			if tool.DefaultAllow {
				tools = append(tools, tool)
			}
		}
		return tools
	}

	// Build map of granted tools for efficient lookup
	grantedMap := make(map[string]bool)
	for _, toolName := range grantedTools {
		grantedMap[strings.TrimSpace(toolName)] = true
	}

	// Return tools that are either granted or default-allow
	var tools []*ToolInfo
	for _, tool := range r.tools {
		if grantedMap[tool.Name] || tool.DefaultAllow {
			tools = append(tools, tool)
		}
	}
	return tools
}

// IsToolGranted checks if a tool is accessible by user
func (r *ToolRegistry) IsToolGranted(toolName string, grantedTools []string) (bool, error) {
	tool, err := r.GetTool(toolName)
	if err != nil {
		return false, err
	}

	// Default-allow tools are always accessible
	if tool.DefaultAllow {
		return true, nil
	}

	// Check if tool is in granted list
	for _, granted := range grantedTools {
		if strings.TrimSpace(granted) == toolName {
			return true, nil
		}
	}

	return false, nil
}

// ValidateToolNames checks if all provided tool names exist
func (r *ToolRegistry) ValidateToolNames(toolNames []string) error {
	for _, name := range toolNames {
		if _, err := r.GetTool(strings.TrimSpace(name)); err != nil {
			return fmt.Errorf("invalid tool: %s", name)
		}
	}
	return nil
}

// FilterUnauthorizedTools returns tools from the input list that are not accessible to the user
func (r *ToolRegistry) FilterUnauthorizedTools(toolNames []string, grantedTools []string) []string {
	var unauthorized []string
	for _, name := range toolNames {
		if granted, _ := r.IsToolGranted(name, grantedTools); !granted {
			unauthorized = append(unauthorized, name)
		}
	}
	return unauthorized
}

// GetUnauthorizedToolError generates a detailed error message for unauthorized tools
func (r *ToolRegistry) GetUnauthorizedToolError(toolNames []string, grantedTools []string) error {
	unauthorized := r.FilterUnauthorizedTools(toolNames, grantedTools)
	if len(unauthorized) == 0 {
		return nil
	}

	if len(unauthorized) == 1 {
		return fmt.Errorf("tool not authorized: %s", unauthorized[0])
	}

	return fmt.Errorf("tools not authorized: %s", strings.Join(unauthorized, ", "))
}
