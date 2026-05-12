-- name: ListUserTeams :many
SELECT
  t.id,
  t.slug,
  t.name,
  t.plan,
  t.archived_at,
  tm.user_id,
  tm.role,
  tm.joined_at,
  tm.invited_at
FROM team_membership tm
JOIN team t ON t.id = tm.team_id
WHERE tm.user_id = $1
  AND ($2::citext IS NULL OR t.slug > $2)
ORDER BY t.slug
LIMIT $3;

-- name: CheckTeamMembership :one
SELECT EXISTS (
  SELECT 1
  FROM team_membership
  WHERE team_id = $1
    AND user_id = $2
);

-- name: GetTeamMembershipRole :one
SELECT role
FROM team_membership
WHERE team_id = $1
  AND user_id = $2;

-- name: ListAppsByTeam :many
SELECT
  id,
  team_id,
  slug,
  name,
  repo_url,
  repo_default_branch,
  image_ref,
  builder,
  status,
  created_at,
  updated_at
FROM app
WHERE team_id = $1
  AND slug > $2
ORDER BY slug
LIMIT $3;

-- name: FindCliTokenByHash :one
SELECT
  id,
  owner_user_id,
  team_id,
  token_hash,
  scopes,
  revoked_at
FROM cli_token
WHERE token_hash = $1;

-- name: IsToolGranted :one
SELECT allowed
FROM tool_grants
WHERE team_id = $1
  AND user_id = $2
  AND tool_id = $3;

-- name: ListGrantedTools :many
SELECT tool_id
FROM tool_grants
WHERE team_id = $1
  AND user_id = $2
  AND allowed = true
ORDER BY tool_id;

-- name: UpsertToolGrant :exec
INSERT INTO tool_grants (team_id, user_id, tool_id, allowed, granted_at, granted_by_actor_id)
VALUES ($1, $2, $3, $4, now(), $5)
ON CONFLICT (team_id, user_id, tool_id) DO UPDATE
SET allowed = $4, granted_at = now(), granted_by_actor_id = $5;

-- name: RevokeToolGrant :exec
DELETE FROM tool_grants
WHERE team_id = $1
  AND user_id = $2
  AND tool_id = $3;

-- name: ListAllUserGrants :many
SELECT tool_id, allowed
FROM tool_grants
WHERE team_id = $1
  AND user_id = $2
ORDER BY tool_id;
