// One-off: ensure admins/{docId} uses an email-shaped doc ID for password reset.
//
// Audit (default):
//
//	cd Prototype-Backend
//	go run ../Random-Scripts/backfill_admin_emails.go
//
// Migrate explicit mappings (dry-run):
//
//	go run ../Random-Scripts/backfill_admin_emails.go -map olduser=user@example.com
//
// Apply migrations:
//
//	go run ../Random-Scripts/backfill_admin_emails.go -apply -map olduser=user@example.com
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
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

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

func parseMappings(values []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid -map %q (want old=new)", v)
		}
		oldID := strings.TrimSpace(parts[0])
		newEmail := normalizeEmail(parts[1])
		if oldID == "" || newEmail == "" {
			return nil, fmt.Errorf("invalid -map %q (empty old or new)", v)
		}
		if !isValidEmail(newEmail) {
			return nil, fmt.Errorf("invalid email in -map %q", v)
		}
		if _, exists := out[oldID]; exists {
			return nil, fmt.Errorf("duplicate -map for %q", oldID)
		}
		out[oldID] = newEmail
	}
	return out, nil
}

func main() {
	apply := flag.Bool("apply", false, "write migrations to Firestore (default: dry-run)")
	mapFlags := flag.String("map", "", "comma-separated old=new mappings (repeat -map or use commas)")
	flag.Parse()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT must be set")
	}

	var rawMaps []string
	if s := strings.TrimSpace(*mapFlags); s != "" {
		rawMaps = append(rawMaps, strings.Split(s, ",")...)
	}
	mappings, err := parseMappings(rawMaps)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer client.Close()

	if len(mappings) == 0 {
		auditAdmins(ctx, client)
		return
	}

	migrateAdmins(ctx, client, mappings, *apply)
}

func auditAdmins(ctx context.Context, client *firestore.Client) {
	iter := client.Collection("admins").Documents(ctx)
	var total, emailIDs, needsMigration int

	fmt.Println("admins audit (doc ID is login + password-reset lookup key):")
	fmt.Println("doc_id\tstored_username\tvalid_email_doc_id")

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("list admins: %v", err)
		}
		total++
		data := doc.Data()
		stored, _ := data["username"].(string)
		valid := isValidEmail(doc.Ref.ID)
		if valid {
			emailIDs++
		} else {
			needsMigration++
		}
		fmt.Printf("%s\t%s\t%t\n", doc.Ref.ID, stored, valid)
	}

	fmt.Printf("\nDone (audit): total=%d valid_email_doc_id=%d needs_migration=%d\n", total, emailIDs, needsMigration)
	if needsMigration > 0 {
		fmt.Println("Re-run with -map olduser=email@domain.com (and -apply when ready) for each non-email doc ID.")
	}
}

func migrateAdmins(ctx context.Context, client *firestore.Client, mappings map[string]string, apply bool) {
	var migrated, skipped, failed int

	for oldID, newEmail := range mappings {
		oldRef := client.Collection("admins").Doc(oldID)
		oldDoc, err := oldRef.Get(ctx)
		if err != nil {
			log.Printf("skip %s: old doc not found: %v", oldID, err)
			failed++
			continue
		}

		newRef := client.Collection("admins").Doc(newEmail)
		if newDoc, err := newRef.Get(ctx); err == nil && newDoc.Exists() {
			if oldID == newEmail {
				log.Printf("ok %s: already at email doc ID", oldID)
				skipped++
				continue
			}
			log.Printf("skip %s -> %s: target doc already exists", oldID, newEmail)
			failed++
			continue
		}

		data := oldDoc.Data()
		data["username"] = newEmail

		log.Printf("migrate %s -> %s", oldID, newEmail)
		migrated++

		if !apply {
			continue
		}

		if _, err := newRef.Set(ctx, data); err != nil {
			log.Fatalf("create %s: %v", newEmail, err)
		}
		if _, err := oldRef.Delete(ctx); err != nil {
			log.Fatalf("delete %s after creating %s: %v", oldID, newEmail, err)
		}
	}

	mode := "dry-run"
	if apply {
		mode = "applied"
	}
	fmt.Printf("\nDone (%s): migrated=%d skipped=%d failed=%d\n", mode, migrated, skipped, failed)
	if !apply && migrated > 0 {
		fmt.Println("Re-run with -apply to write changes. Affected users must log in with the new email doc ID.")
	}
}
