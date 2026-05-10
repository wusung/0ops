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
