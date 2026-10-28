package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/engine/archive"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// archiver is where the scheduler keeps runs past retention before it
// purges them (spec 4.11, 9.4): an S3-compatible bucket when
// TASKIEM_ARCHIVE_S3_BUCKET is set, else TASKIEM_ARCHIVE_DIR on a mounted
// volume, else nowhere, and then nothing is purged. A bucket wins over a
// directory, so a chart that always sets the directory can add a bucket.
func archiver(getenv func(string) string, log *slog.Logger) (runtime.Archiver, error) {
	bucket := getenv("TASKIEM_ARCHIVE_S3_BUCKET")
	dir := getenv("TASKIEM_ARCHIVE_DIR")
	if bucket == "" {
		for _, k := range []string{"TASKIEM_ARCHIVE_S3_ENDPOINT", "TASKIEM_ARCHIVE_S3_ACCESS_KEY_ID", "TASKIEM_ARCHIVE_S3_SECRET_ACCESS_KEY"} {
			if getenv(k) != "" {
				return nil, fmt.Errorf("%s is set but TASKIEM_ARCHIVE_S3_BUCKET is not: name the bucket runs are archived to", k)
			}
		}
		if dir == "" {
			log.Warn("TASKIEM_ARCHIVE_S3_BUCKET and TASKIEM_ARCHIVE_DIR unset: runs past retention are kept, not purged")
			return nil, nil
		}
		log.Warn("runs past retention are archived to a local directory; with more than one scheduler, or to keep them past this machine, set TASKIEM_ARCHIVE_S3_BUCKET", "dir", dir)
		return runtime.FileArchiver{Dir: dir}, nil
	}
	region := getenv("TASKIEM_ARCHIVE_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	endpoint := getenv("TASKIEM_ARCHIVE_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://s3." + region + ".amazonaws.com"
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("TASKIEM_ARCHIVE_S3_ENDPOINT: %w", err)
	}
	prefix := getenv("TASKIEM_ARCHIVE_S3_PREFIX")
	if prefix == "" {
		prefix = "runs/"
	}
	pathStyle := false
	if v := getenv("TASKIEM_ARCHIVE_S3_PATH_STYLE"); v != "" {
		if pathStyle, err = strconv.ParseBool(v); err != nil {
			return nil, fmt.Errorf("TASKIEM_ARCHIVE_S3_PATH_STYLE: %q is not true or false", v)
		}
	}
	a := &archive.S3{
		Endpoint:  u,
		Bucket:    bucket,
		Prefix:    prefix,
		PathStyle: pathStyle,
		Signer: s3.Signer{
			AccessKeyID:     getenv("TASKIEM_ARCHIVE_S3_ACCESS_KEY_ID"),
			SecretAccessKey: getenv("TASKIEM_ARCHIVE_S3_SECRET_ACCESS_KEY"),
			SessionToken:    getenv("TASKIEM_ARCHIVE_S3_SESSION_TOKEN"),
			Region:          region,
			Service:         "s3",
		},
		SSE:      getenv("TASKIEM_ARCHIVE_S3_SSE"),
		KMSKeyID: getenv("TASKIEM_ARCHIVE_S3_KMS_KEY_ID"),
	}
	if err := a.Validate(); err != nil {
		return nil, errors.New(strings.Replace(err.Error(), "archive: ", "TASKIEM_ARCHIVE_S3_*: ", 1))
	}
	if dir != "" {
		log.Info("TASKIEM_ARCHIVE_S3_BUCKET is set, so TASKIEM_ARCHIVE_DIR is not used for archives", "dir", dir)
	}
	log.Info("runs past retention are archived to a bucket", "endpoint", u.Redacted(), "bucket", bucket, "prefix", prefix, "path_style", a.PathStyle)
	return a, nil
}
