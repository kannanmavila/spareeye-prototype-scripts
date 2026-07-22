// Upload phone filler WAVs to GCS and register Firestore docs for a surface.
//
// Usage:
//
//	go run upload_phone_fillers.go -surface internal-bliss-test-do-not-share -dir ~/Desktop/fillers
//
// Requires GOOGLE_CLOUD_PROJECT and Application Default Credentials.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
)

const bucketName = "spareeye-prototype-image-uploads-d27e6eae"

func main() {
	surfaceID := flag.String("surface", "", "surface ID (required)")
	dir := flag.String("dir", "", "directory with female/ and male/ subfolders (required)")
	flag.Parse()
	if strings.TrimSpace(*surfaceID) == "" || strings.TrimSpace(*dir) == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx := context.Background()
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT is required")
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("storage client: %v", err)
	}
	defer storageClient.Close()

	firestoreClient, err := firestore.NewClient(ctx, project)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer firestoreClient.Close()

	uploadDir(ctx, storageClient, firestoreClient, *surfaceID, *dir, "female", "feminine")
	uploadDir(ctx, storageClient, firestoreClient, *surfaceID, *dir, "male", "masculine")
}

func uploadDir(ctx context.Context, storageClient *storage.Client, firestoreClient *firestore.Client, surfaceID, root, folder, gender string) {
	path := filepath.Join(root, folder)
	entries, err := os.ReadDir(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".wav") {
			continue
		}
		if count >= 5 {
			log.Printf("skip %s (max 5 per gender)", e.Name())
			continue
		}
		localPath := filepath.Join(path, e.Name())
		objectPath := fmt.Sprintf("telephony/fillers/%s/%s/%s", surfaceID, gender, e.Name())
		gcsPath := fmt.Sprintf("gs://%s/%s", bucketName, objectPath)

		data, err := os.ReadFile(localPath)
		if err != nil {
			log.Fatalf("read %s: %v", localPath, err)
		}
		w := storageClient.Bucket(bucketName).Object(objectPath).NewWriter(ctx)
		w.ContentType = "audio/wav"
		if _, err := w.Write(data); err != nil {
			_ = w.Close()
			log.Fatalf("write gcs %s: %v", gcsPath, err)
		}
		if err := w.Close(); err != nil {
			log.Fatalf("close gcs %s: %v", gcsPath, err)
		}

		docID := fmt.Sprintf("%s_%s", strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), gender)
		_, err = firestoreClient.Collection("surfaces").Doc(surfaceID).Collection("phoneFillers").Doc(docID).Set(ctx, map[string]interface{}{
			"gcsPath": gcsPath,
			"gender":  gender,
		})
		if err != nil {
			log.Fatalf("firestore %s: %v", docID, err)
		}
		log.Printf("uploaded %s -> %s (%s)", localPath, gcsPath, gender)
		count++
	}
}
