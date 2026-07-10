// One-off: create organizations and link admins via organizationId.
//
// Org doc ids are UUIDs. Omit id= to auto-generate one.
//
// Dry-run (default):
//
//	cd Prototype-Backend
//	go run ../Random-Scripts/setup_organizations.go \
//	  -org name="Bliss Fertility",prefix=bliss
//
// Apply:
//
//	go run ../Random-Scripts/setup_organizations.go -apply \
//	  -org name="Bliss Fertility",prefix=bliss \
//	  -admin email=alice@example.com,org=<uuid-from-create-output>
//
// Requires GOOGLE_CLOUD_PROJECT and Application Default Credentials.
//
//go:build ignore

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/mail"
	"os"
	"regexp"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var surfaceSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty flag value")
	}
	*s = append(*s, value)
	return nil
}

type orgSpec struct {
	ID     string
	Name   string
	Prefix string
}

type adminSpec struct {
	Email string
	OrgID string
}

func normalizeEmail(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}

func isValidEmail(addr string) bool {
	addr = normalizeEmail(addr)
	if addr == "" {
		return false
	}
	_, err := mail.ParseAddress(addr)
	return err == nil
}

func normalizeSurfaceSlug(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.ReplaceAll(s, " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func validateSurfaceSlug(slug string) error {
	if len(slug) < 2 {
		return fmt.Errorf("slug must be at least 2 characters")
	}
	if !surfaceSlugPattern.MatchString(slug) {
		return fmt.Errorf("slug must use lowercase letters, numbers, and hyphens only")
	}
	return nil
}

func validateOrgID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("org id must be a UUID: %w", err)
	}
	return nil
}

func parseKV(spec string) (map[string]string, error) {
	out := make(map[string]string)
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid spec %q (want key=value)", spec)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key == "" || val == "" {
			return nil, fmt.Errorf("invalid spec %q (empty key or value)", spec)
		}
		out[key] = val
	}
	return out, nil
}

func parseOrgSpec(raw string) (orgSpec, error) {
	kv, err := parseKV(raw)
	if err != nil {
		return orgSpec{}, err
	}
	id := strings.TrimSpace(kv["id"])
	name := strings.TrimSpace(kv["name"])
	prefix := normalizeSurfaceSlug(kv["prefix"])
	if name == "" || prefix == "" {
		return orgSpec{}, fmt.Errorf("org spec %q requires name and prefix", raw)
	}
	if id == "" {
		id = uuid.NewString()
	} else if err := validateOrgID(id); err != nil {
		return orgSpec{}, fmt.Errorf("org spec %q: %w", raw, err)
	}
	if err := validateSurfaceSlug(prefix); err != nil {
		return orgSpec{}, fmt.Errorf("org prefix %q: %w", prefix, err)
	}
	return orgSpec{ID: id, Name: name, Prefix: prefix}, nil
}

func parseAdminSpec(raw string) (adminSpec, error) {
	kv, err := parseKV(raw)
	if err != nil {
		return adminSpec{}, err
	}
	email := normalizeEmail(kv["email"])
	orgID := strings.TrimSpace(kv["org"])
	if email == "" || orgID == "" {
		return adminSpec{}, fmt.Errorf("admin spec %q requires email and org", raw)
	}
	if !isValidEmail(email) {
		return adminSpec{}, fmt.Errorf("invalid email in admin spec %q", raw)
	}
	if err := validateOrgID(orgID); err != nil {
		return adminSpec{}, fmt.Errorf("admin spec %q: %w", raw, err)
	}
	return adminSpec{Email: email, OrgID: orgID}, nil
}

func main() {
	apply := flag.Bool("apply", false, "write changes to Firestore (default: dry-run)")
	var orgFlags stringList
	var adminFlags stringList
	flag.Var(&orgFlags, "org", "organization spec: name=...,prefix=...[,id=<uuid>] (repeatable)")
	flag.Var(&adminFlags, "admin", "admin spec: email=...,org=<uuid> (repeatable)")
	flag.Parse()

	if len(orgFlags) == 0 && len(adminFlags) == 0 {
		log.Fatal("provide at least one -org and/or -admin flag")
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT must be set")
	}

	orgs := make(map[string]orgSpec)
	for _, raw := range orgFlags {
		spec, err := parseOrgSpec(raw)
		if err != nil {
			log.Fatal(err)
		}
		if _, exists := orgs[spec.ID]; exists {
			log.Fatalf("duplicate -org for id %q", spec.ID)
		}
		orgs[spec.ID] = spec
	}

	admins := make([]adminSpec, 0, len(adminFlags))
	for _, raw := range adminFlags {
		spec, err := parseAdminSpec(raw)
		if err != nil {
			log.Fatal(err)
		}
		admins = append(admins, spec)
	}

	for _, admin := range admins {
		if _, ok := orgs[admin.OrgID]; !ok && len(orgFlags) > 0 {
			log.Fatalf("admin %s references unknown org %q (not in -org flags)", admin.Email, admin.OrgID)
		}
	}

	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer client.Close()

	var orgCreates, adminUpdates, skipped int

	for _, org := range orgs {
		ref := client.Collection("organizations").Doc(org.ID)
		_, err := ref.Get(ctx)
		if err == nil {
			fmt.Printf("skip org %s: already exists\n", org.ID)
			skipped++
			continue
		}
		if status.Code(err) != codes.NotFound {
			log.Fatalf("get organizations/%s: %v", org.ID, err)
		}

		fmt.Printf("create organizations/%s name=%q prefix=%q\n", org.ID, org.Name, org.Prefix)
		orgCreates++

		if !*apply {
			continue
		}

		if _, err := ref.Set(ctx, map[string]interface{}{
			"name":   org.Name,
			"prefix": org.Prefix,
		}); err != nil {
			log.Fatalf("create organizations/%s: %v", org.ID, err)
		}
	}

	for _, admin := range admins {
		ref := client.Collection("admins").Doc(admin.Email)
		doc, err := ref.Get(ctx)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				log.Fatalf("admin %s not found", admin.Email)
			}
			log.Fatalf("get admins/%s: %v", admin.Email, err)
		}

		current, _ := doc.Data()["organizationId"].(string)
		if current == admin.OrgID {
			fmt.Printf("skip admin %s: already organizationId=%q\n", admin.Email, admin.OrgID)
			skipped++
			continue
		}
		if current != "" && current != admin.OrgID {
			log.Fatalf("admin %s already has organizationId=%q (refusing to overwrite with %q)", admin.Email, current, admin.OrgID)
		}

		fmt.Printf("set admins/%s organizationId=%q\n", admin.Email, admin.OrgID)
		adminUpdates++

		if !*apply {
			continue
		}

		if _, err := ref.Update(ctx, []firestore.Update{
			{Path: "organizationId", Value: admin.OrgID},
		}); err != nil {
			log.Fatalf("update admins/%s: %v", admin.Email, err)
		}
	}

	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("\nDone (%s): org_creates=%d admin_updates=%d skipped=%d\n", mode, orgCreates, adminUpdates, skipped)
	if !*apply && (orgCreates > 0 || adminUpdates > 0) {
		fmt.Println("Re-run with -apply to write changes.")
	}
}
