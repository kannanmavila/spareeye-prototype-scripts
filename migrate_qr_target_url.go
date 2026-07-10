// One-off: set surfaces.qrCode.targetUrl to https://app.shelftalker.ai/{surfaceId}
// for every surface document that has a qrCode field.
//
// Usage (dry-run by default):
//
//	cd Prototype-Backend
//	go run ../Random-Scripts/migrate_qr_target_url.go
//
// Apply writes:
//
//	go run ../Random-Scripts/migrate_qr_target_url.go -apply
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
	"google.golang.org/api/iterator"
)

func main() {
	apply := flag.Bool("apply", false, "write updates to Firestore (default: dry-run)")
	appOrigin := flag.String("app-origin", "https://app.shelftalker.ai", "PUBLIC_APP_ORIGIN base (no trailing slash)")
	flag.Parse()

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

	base := strings.TrimRight(*appOrigin, "/") + "/"

	iter := client.Collection("surfaces").Documents(ctx)
	var scanned, withQR, updated int

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("list surfaces: %v", err)
		}
		scanned++
		data := doc.Data()
		qrRaw, ok := data["qrCode"]
		if !ok {
			continue
		}
		qrMap, ok := qrRaw.(map[string]interface{})
		if !ok {
			log.Printf("skip %s: qrCode is not a map", doc.Ref.ID)
			continue
		}
		withQR++

		newURL := base + doc.Ref.ID
		oldURL, _ := qrMap["targetUrl"].(string)
		if oldURL == newURL {
			log.Printf("ok %s: already %s", doc.Ref.ID, newURL)
			continue
		}

		log.Printf("update %s: %q -> %q", doc.Ref.ID, oldURL, newURL)
		updated++

		if *apply {
			_, err := doc.Ref.Update(ctx, []firestore.Update{
				{Path: "qrCode.targetUrl", Value: newURL},
			})
			if err != nil {
				log.Fatalf("update %s: %v", doc.Ref.ID, err)
			}
		}
	}

	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("\nDone (%s): scanned=%d with_qrCode=%d would_update=%d\n", mode, scanned, withQR, updated)
	if !*apply && updated > 0 {
		fmt.Println("Re-run with -apply to write changes. Regenerate QRs in admin for scannable PNGs.")
	}
}
