# Input: {"workspace": {"id"}, "user": {"id", "email", "domain"}}
# The rule must define a boolean `allow` in package papers.authz.
package papers.authz

default allow := false

allow if input.user.domain == "example.com"

allow if input.user.email in {"contractor@partner.example"}

# the --no-authn development user (remove in production)
allow if input.user.email == "dev@localhost"
