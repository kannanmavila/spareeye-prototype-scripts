// One-off: move an organization doc from a legacy slug id to a UUID doc id.
//
// Dry-run:
//
//	cd Prototype-Backend
//	go run ../Random-Scripts/migrate_organization_to_uuid.go -from bliss-fertility
//
// Apply:
//
//	go run ../Random-Scripts/migrate_organization_to_uuid.go -apply -from bliss-fertility
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
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	apply := flag.Bool("apply", false, "write changes to Firestore (default: dry-run)")
	fromID := flag.String("from", "", "legacy organizations/{id} doc id to migrate (required)")
	toID := flag.String("to", "", "target UUID (default: generate a new UUID)")
	flag.Parse()

	from := strings.TrimSpace(*fromID)
	if from == "" {
		log.Fatal("-from is required")
	}
	if _, err := uuid.Parse(from); err == nil {
		log.Fatalf("-from %q already looks like a UUID; nothing to migrate", from)
	}

	target := strings.TrimSpace(*toID)
	if target == "" {
		target = uuid.NewString()
	} else if _, err := uuid.Parse(target); err != nil {
		log.Fatalf("-to %q is not a valid UUID: %v", target, err)
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT must be set")
	}

	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer client.Close()

	oldRef := client.Collection("organizations").Doc(from)
	oldDoc, err := oldRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			log.Fatalf("organizations/%s not found", from)
		}
		log.Fatalf("get organizations/%s: %v", from, err)
	}

	newRef := client.Collection("organizations").Doc(target)
	if newDoc, err := newRef.Get(ctx); err == nil && newDoc.Exists() {
		log.Fatalf("organizations/%s already exists", target)
	} else if err != nil && status.Code(err) != codes.NotFound {
		log.Fatalf("get organizations/%s: %v", target, err)
	}

	data := oldDoc.Data()
	name, _ := data["name"].(string)
	prefix, _ := data["prefix"].(string)

	var adminEmails []string
	adminIter := client.Collection("admins").Where("organizationId", "==", from).Documents(ctx)
	for {
		doc, err := adminIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("list admins for org %s: %v", from, err)
		}
		adminEmails = append(adminEmails, doc.Ref.ID)
	}

	fmt.Printf("migrate organizations/%s -> organizations/%s (name=%q prefix=%q)\n", from, target, name, prefix)
	if len(adminEmails) == 0 {
		fmt.Println("no admins reference the legacy org id")
	} else {
		fmt.Println("update admins.organizationId:")
		for _, email := range adminEmails {
			fmt.Printf("  %s: %q -> %q\n", email, from, target)
		}
	}
	fmt.Printf("delete organizations/%s\n", from)

	if !*apply {
		fmt.Println("\nRe-run with -apply to write changes.")
		fmt.Printf("Use organizationId %q when linking admins.\n", target)
		return
	}

	if _, err := newRef.Set(ctx, data); err != nil {
		log.Fatalf("create organizations/%s: %v", target, err)
	}

	for _, email := range adminEmails {
		adminRef := client.Collection("admins").Doc(email)
		if _, err := adminRef.Update(ctx, []firestore.Update{
			{Path: "organizationId", Value: target},
		}); err != nil {
			log.Fatalf("update admins/%s: %v", email, err)
		}
	}

	if _, err := oldRef.Delete(ctx); err != nil {
		log.Fatalf("delete organizations/%s: %v", from, err)
	}

	fmt.Printf("\nDone (applied). New organizationId: %s\n", target)
}
