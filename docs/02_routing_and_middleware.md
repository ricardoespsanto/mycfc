# Task: Setup Router and Role-Based Middleware

Write the Go code for setting up the `net/http` ServeMux using Go 1.22 path matching features. 

1. Create a middleware package (`internal/auth`) that checks user sessions and roles.
2. Define the main router in `cmd/server/main.go` or a dedicated `routes.go` file.
3. Set up the following route groups:
   * Public: `/login`, `/public-news`
   * Authenticated (Base): `/dashboard` (This handler should inspect the user's role and redirect to the specific dashboards below)
   * Role-Restricted: 
     * `/dashboard/competitor`
     * `/dashboard/leisure`
     * `/dashboard/guardian`
     * `/admin/fleet`
     * `/admin/repairs`

Please provide the implementation for the router setup and the role-checking middleware.
