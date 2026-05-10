package rbac

// Role is the team membership role.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// AtLeast reports whether the role meets the minimum role.
func AtLeast(have, need Role) bool {
	order := map[Role]int{
		RoleViewer: 0,
		RoleMember: 1,
		RoleAdmin:  2,
		RoleOwner:  3,
	}
	return order[have] >= order[need]
}
