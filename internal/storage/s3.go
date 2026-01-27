package storage

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

type S3Storage struct {
	client   *s3.Client
	bucket   string
	cdnURL   string // Public URL prefix for serving files
	endpoint string
}

type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	CDNURL          string // Optional CDN URL, defaults to endpoint/bucket
}

func NewS3Storage(cfg Config) (*S3Storage, error) {
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: true, // Required for most S3-compatible services
	})

	cdnURL := cfg.CDNURL
	if cdnURL == "" {
		cdnURL = fmt.Sprintf("%s/%s", cfg.Endpoint, cfg.Bucket)
	}

	return &S3Storage{
		client:   client,
		bucket:   cfg.Bucket,
		cdnURL:   cdnURL,
		endpoint: cfg.Endpoint,
	}, nil
}

// Upload uploads a file and returns the public URL
func (s *S3Storage) Upload(ctx context.Context, folder string, filename string, contentType string, reader io.Reader) (string, error) {
	// Generate unique filename to avoid collisions
	ext := path.Ext(filename)
	uniqueName := fmt.Sprintf("%s/%s%s", folder, uuid.New().String(), ext)

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(uniqueName),
		Body:        reader,
		ContentType: aws.String(contentType),
		ACL:         types.ObjectCannedACLPublicRead,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}

	// Return public URL
	publicURL := fmt.Sprintf("%s/%s", strings.TrimSuffix(s.cdnURL, "/"), uniqueName)
	return publicURL, nil
}

// UploadAvatar uploads an avatar image
func (s *S3Storage) UploadAvatar(ctx context.Context, userID uuid.UUID, filename string, contentType string, reader io.Reader) (string, error) {
	// Validate content type
	if !isValidImageType(contentType) {
		return "", fmt.Errorf("invalid image type: %s", contentType)
	}

	folder := fmt.Sprintf("avatars/%s", userID.String())
	return s.Upload(ctx, folder, filename, contentType, reader)
}

// Delete deletes a file by its URL
func (s *S3Storage) Delete(ctx context.Context, fileURL string) error {
	// Extract key from URL
	key := strings.TrimPrefix(fileURL, s.cdnURL+"/")
	if key == fileURL {
		// Try alternative format
		key = strings.TrimPrefix(fileURL, fmt.Sprintf("%s/%s/", s.endpoint, s.bucket))
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// GetPresignedURL generates a presigned URL for direct upload (optional, for client-side uploads)
func (s *S3Storage) GetPresignedURL(ctx context.Context, key string, contentType string, expiresIn time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)

	request, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expiresIn))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return request.URL, nil
}

func isValidImageType(contentType string) bool {
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}
	return validTypes[contentType]
}
