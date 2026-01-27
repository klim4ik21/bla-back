package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()

	// Config
	dbURL := "postgresql://bla:bla@localhost:5432/bla"
	s3Endpoint := "https://s3.twcstorage.ru"
	s3Region := "ru-1"
	s3Bucket := "f5d9c802-spb1"
	s3AccessKey := "MYRENGLV1CE5YWB4G8BF"
	s3SecretKey := "KphWppiBgaPUMWZp1xdaXc7H5CcNxNBz22BDeHJO"

	// Sticker directory from command line or default
	stickerDir := "/Users/klimentiy/Проекты/bla/stickers/neko_webm_v1"
	packName := "Neko Pack"
	if len(os.Args) > 1 {
		stickerDir = os.Args[1]
	}
	if len(os.Args) > 2 {
		packName = os.Args[2]
	}

	// Connect to DB
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Create S3 client
	s3Client := s3.New(s3.Options{
		Region:       s3Region,
		BaseEndpoint: aws.String(s3Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(s3AccessKey, s3SecretKey, ""),
		UsePathStyle: true,
	})

	// Create official sticker pack
	packID := uuid.New()
	packDesc := "Animated sticker pack"

	_, err = pool.Exec(ctx, `
		INSERT INTO sticker_packs (id, name, description, is_official, created_at, updated_at)
		VALUES ($1, $2, $3, true, NOW(), NOW())
	`, packID, packName, packDesc)
	if err != nil {
		log.Fatalf("Failed to create sticker pack: %v", err)
	}
	log.Printf("Created sticker pack: %s (%s)", packName, packID)

	// Get all sticker files
	files, err := os.ReadDir(stickerDir)
	if err != nil {
		log.Fatalf("Failed to read sticker directory: %v", err)
	}

	// Sort files by name
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// Emoji list for stickers
	emojis := []string{"😺", "😸", "😹", "😻", "😼", "😽", "🙀", "😿", "😾", "🐱",
		"🐈", "🐈‍⬛", "👋", "✨", "💕", "💖", "💗", "💝", "💘", "❤️",
		"🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔", "❤️‍🔥",
		"❤️‍🩹", "💯", "💢", "💥", "💫", "💦", "💨", "🕳️", "💣", "💬",
		"👁️‍🗨️", "🗨️", "🗯️", "💭", "💤", "🎉", "🎊", "🎈", "🎁", "🎀"}

	var firstStickerURL string
	count := 0

	for i, file := range files {
		if file.IsDir() {
			continue
		}

		filename := file.Name()
		ext := strings.ToLower(filepath.Ext(filename))

		var fileType, contentType string
		switch ext {
		case ".tgs":
			fileType = "tgs"
			contentType = "application/gzip"
		case ".webm":
			fileType = "webm"
			contentType = "video/webm"
		case ".webp":
			fileType = "webp"
			contentType = "image/webp"
		case ".png":
			fileType = "png"
			contentType = "image/png"
		default:
			log.Printf("Skipping unsupported file: %s", filename)
			continue
		}

		// Read file
		filePath := filepath.Join(stickerDir, filename)
		fileData, err := os.Open(filePath)
		if err != nil {
			log.Printf("Failed to open %s: %v", filename, err)
			continue
		}

		// Upload to S3
		s3Key := fmt.Sprintf("stickers/%s/%s%s", packID, uuid.New().String(), ext)

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s3Bucket),
			Key:         aws.String(s3Key),
			Body:        fileData,
			ContentType: aws.String(contentType),
			ACL:         types.ObjectCannedACLPublicRead,
		})
		fileData.Close()

		if err != nil {
			log.Printf("Failed to upload %s: %v", filename, err)
			continue
		}

		fileURL := fmt.Sprintf("%s/%s/%s", s3Endpoint, s3Bucket, s3Key)

		if firstStickerURL == "" {
			firstStickerURL = fileURL
		}

		// Get emoji
		emoji := "😺"
		if i < len(emojis) {
			emoji = emojis[i]
		}

		// Insert sticker record
		stickerID := uuid.New()
		_, err = pool.Exec(ctx, `
			INSERT INTO stickers (id, pack_id, emoji, file_url, file_type, width, height, created_at)
			VALUES ($1, $2, $3, $4, $5, 512, 512, NOW())
		`, stickerID, packID, emoji, fileURL, fileType)
		if err != nil {
			log.Printf("Failed to insert sticker %s: %v", filename, err)
			continue
		}

		count++
		log.Printf("Uploaded: %s (%s)", filename, emoji)
	}

	// Update pack cover
	if firstStickerURL != "" {
		_, err = pool.Exec(ctx, `UPDATE sticker_packs SET cover_url = $1 WHERE id = $2`, firstStickerURL, packID)
		if err != nil {
			log.Printf("Warning: failed to set pack cover: %v", err)
		}
	}

	log.Printf("Done! Uploaded %d stickers to pack '%s'", count, packName)
}
