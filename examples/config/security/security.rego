# Input: {"workspace": {"id"}, "user": {"id", "email", "domain", "anonymous"}}
# Anonymous users (--anonymous) have the domain "anonymous.invalid"; to let
# them in, add: allow if input.user.anonymous
# The rule must define a boolean `allow` in package papers.authz.
package papers.authz

default allow := false

allow if input.user.domain == "example.com"

allow if input.user.email in {"contractor@partner.example"}

# the --no-authn development user (remove in production)
allow if input.user.email == "dev@localhost"
