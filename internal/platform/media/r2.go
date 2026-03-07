package media

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Client wraps Cloudflare R2 (S3-compatible) for object storage.
// Used for shipping labels and other private binary assets.
type R2Client struct {
	s3Client     *s3.Client
	presignClient *s3.PresignClient
	bucket       string
}

// R2Config holds the credentials and endpoint for Cloudflare R2.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey  string
	Bucket          string
}

// NewR2Client creates an R2 client using S3-compatible SDK.
func NewR2Client(ctx context.Context, cfg R2Config) (*R2Client, error) {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("r2 load aws config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &R2Client{
		s3Client:     s3Client,
		presignClient: s3.NewPresignClient(s3Client),
		bucket:       cfg.Bucket,
	}, nil
}

// PutObject uploads bytes to R2.
func (c *R2Client) PutObject(ctx context.Context, key string, body []byte, contentType string) error {
	_, err := c.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("r2 put object %s: %w", key, err)
	}
	return nil
}

// PresignGetURL returns a time-limited download URL for a private R2 object.
func (c *R2Client) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := c.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("r2 presign get %s: %w", key, err)
	}
	return req.URL, nil
}
