# Neo Box — Domain Context

Neo Box is a personal system for managing and viewing resources held in
third-party services (cloud providers, SaaS accounts, subscriptions, …) from
one place.

## Terms

- **User** — a person who signs in to Neo Box. Role `admin` can manage other
  users; role `user` manages only their own data. Users sign in with a
  password or an OAuth provider (GitHub, Google).
- **Session** — a bearer token issued at sign-in. Only its sha256 is stored;
  it expires after `auth.session_ttl`.

## Planned (not yet modeled)

- **Provider** — a kind of third-party service Neo Box knows how to talk to.
- **Connection** — a user's credentials for one Provider account.
- **Resource** — one item fetched through a Connection (e.g. a server, a
  domain, a subscription), shown and tracked in the dashboard.

Define these precisely here before implementing them.
