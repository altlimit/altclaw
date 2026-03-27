package bridge

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsCreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dop251/goja"
	"gocloud.dev/blob"
	"gocloud.dev/blob/azureblob"
	"gocloud.dev/blob/gcsblob"
	"gocloud.dev/blob/s3blob"
	"gocloud.dev/gcp"
	"golang.org/x/oauth2/google"

	azblob "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	azcontainer "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// RegisterBlob adds the blob namespace to the runtime.
func RegisterBlob(vm *goja.Runtime, pool *BlobPool, store *config.Store, workspace string, ctxFn ...func() context.Context) {
	blobObj := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	// blob.open(driver, bucket, opts) → handle object
	blobObj.Set("open", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "blob.open requires (driver, bucket, opts)")
		}
		ctx := getCtx()
		driver := call.Arguments[0].String()
		bucketName := call.Arguments[1].String()
		opts := call.Arguments[2].ToObject(vm)

		// Expand secrets in all option values
		getOpt := func(key string) string {
			v := opts.Get(key)
			if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
				return ""
			}
			return ExpandSecrets(ctx, store, v.String())
		}

		// Check pool for existing connection
		poolKey := driver + ":" + bucketName
		if existing := pool.Get(poolKey); existing != nil {
			return makeBlobHandle(vm, existing, pool, poolKey, workspace)
		}

		var bucket *blob.Bucket
		var err error

		switch strings.ToLower(driver) {
		case "s3":
			bucket, err = openS3Bucket(ctx, bucketName, getOpt)
		case "gs", "gcs":
			bucket, err = openGCSBucket(ctx, bucketName, getOpt)
		case "azblob", "azure":
			bucket, err = openAzureBucket(ctx, bucketName, getOpt)
		default:
			Throwf(vm, "blob.open: unsupported driver %q (use s3, gs, or azblob)", driver)
		}
		if err != nil {
			logErr(vm, "blob.open", err)
		}

		pool.Put(poolKey, bucket)
		return makeBlobHandle(vm, bucket, pool, poolKey, workspace)
	})

	// blob.transfer(srcHandle, srcKey, dstHandle, dstKey) — cross-bucket streaming copy
	blobObj.Set("transfer", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 4 {
			Throw(vm, "blob.transfer requires (srcHandle, srcKey, dstHandle, dstKey)")
		}
		srcObj := call.Arguments[0].ToObject(vm)
		srcKey := call.Arguments[1].String()
		dstObj := call.Arguments[2].ToObject(vm)
		dstKey := call.Arguments[3].String()

		// Retrieve bucket references from the handle objects
		srcBucketVal := srcObj.Get("__bucket")
		dstBucketVal := dstObj.Get("__bucket")
		if srcBucketVal == nil || dstBucketVal == nil {
			Throw(vm, "blob.transfer: invalid bucket handles")
		}

		srcBucket, ok := srcBucketVal.Export().(*blob.Bucket)
		if !ok {
			Throw(vm, "blob.transfer: invalid source handle")
		}
		dstBucket, ok := dstBucketVal.Export().(*blob.Bucket)
		if !ok {
			Throw(vm, "blob.transfer: invalid destination handle")
		}

		ctx := context.Background()
		reader, err := srcBucket.NewReader(ctx, srcKey, nil)
		if err != nil {
			logErr(vm, "blob.transfer", err)
		}
		defer reader.Close()

		writer, err := dstBucket.NewWriter(ctx, dstKey, nil)
		if err != nil {
			logErr(vm, "blob.transfer", err)
		}

		n, err := io.Copy(writer, reader)
		if err != nil {
			writer.Close()
			logErr(vm, "blob.transfer", err)
		}
		if err := writer.Close(); err != nil {
			logErr(vm, "blob.transfer", err)
		}

		result := vm.NewObject()
		result.Set("bytes", n)
		return result
	})

	// blob.connections() → array of active keys
	blobObj.Set("connections", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(pool.List())
	})

	vm.Set(NameBlob, blobObj)
}

// openS3Bucket opens an S3-compatible bucket with explicit credentials.
func openS3Bucket(ctx context.Context, bucketName string, getOpt func(string) string) (*blob.Bucket, error) {
	accessKey := getOpt("accessKey")
	secretKey := getOpt("secretKey")
	region := getOpt("region")

	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("s3: accessKey and secretKey are required")
	}
	if region == "" {
		region = "us-east-1"
	}

	creds := awsCreds.NewStaticCredentialsProvider(accessKey, secretKey, getOpt("sessionToken"))
	cfg := aws.Config{
		Region:      region,
		Credentials: creds,
	}

	// Support custom endpoint for S3-compatible stores (MinIO, R2, etc.)
	endpoint := getOpt("endpoint")
	s3Opts := []func(*s3.Options){}
	if endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(cfg, s3Opts...)
	return s3blob.OpenBucketV2(ctx, client, bucketName, nil)
}

// openGCSBucket opens a GCS bucket with explicit credentials JSON.
func openGCSBucket(ctx context.Context, bucketName string, getOpt func(string) string) (*blob.Bucket, error) {
	credsJSON := getOpt("credentialsJSON")
	if credsJSON == "" {
		return nil, fmt.Errorf("gs: credentialsJSON is required (base64-encoded service account key)")
	}

	// Decode base64
	jsonBytes, err := base64.StdEncoding.DecodeString(credsJSON)
	if err != nil {
		// Try raw JSON (not base64-encoded)
		jsonBytes = []byte(credsJSON)
	}

	creds, err := google.CredentialsFromJSON(ctx, jsonBytes, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("gs: failed to parse credentials: %w", err)
	}

	tokenSource := gcp.CredentialsTokenSource(creds)
	httpClient, err := gcp.NewHTTPClient(gcp.DefaultTransport(), tokenSource)
	if err != nil {
		return nil, fmt.Errorf("gs: failed to create HTTP client: %w", err)
	}

	return gcsblob.OpenBucket(ctx, httpClient, bucketName, nil)
}

// openAzureBucket opens an Azure Blob Storage container with explicit credentials.
func openAzureBucket(ctx context.Context, containerName string, getOpt func(string) string) (*blob.Bucket, error) {
	accountName := getOpt("accountName")
	accountKey := getOpt("accountKey")

	if accountName == "" || accountKey == "" {
		return nil, fmt.Errorf("azblob: accountName and accountKey are required")
	}

	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", accountName)

	cred, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		return nil, fmt.Errorf("azblob: invalid credentials: %w", err)
	}

	client, err := azcontainer.NewClientWithSharedKeyCredential(serviceURL+containerName, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azblob: failed to create client: %w", err)
	}

	return azureblob.OpenBucket(ctx, client, nil)
}

// makeBlobHandle builds a JS object with methods bound to the given *blob.Bucket.
func makeBlobHandle(vm *goja.Runtime, bucket *blob.Bucket, pool *BlobPool, key, workspace string) goja.Value {
	handle := vm.NewObject()

	// Expose bucket reference for blob.transfer() cross-bucket copy
	handle.Set("__bucket", bucket)

	// handle.read(key, opts?) → string | ArrayBuffer
	// opts.offset   — byte offset to start reading from (default: 0)
	// opts.length   — number of bytes to read (default: -1 = entire blob)
	// opts.encoding — "buffer" to return an ArrayBuffer (for binary blobs)
	handle.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "blob.read requires a key argument")
		}
		k := call.Arguments[0].String()

		var offset int64
		length := int64(-1) // -1 = read to end
		encoding := ""

		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			optsObj := call.Arguments[1].ToObject(vm)
			if v := optsObj.Get("offset"); v != nil && !goja.IsUndefined(v) {
				offset = v.ToInteger()
			}
			if v := optsObj.Get("length"); v != nil && !goja.IsUndefined(v) {
				length = v.ToInteger()
			}
			if v := optsObj.Get("encoding"); v != nil && !goja.IsUndefined(v) {
				encoding = v.String()
			}
		}

		ctx := context.Background()
		var data []byte

		if offset != 0 || length >= 0 {
			// Range read via NewRangeReader
			reader, err := bucket.NewRangeReader(ctx, k, offset, length, nil)
			if err != nil {
				logErr(vm, "blob.read", err)
			}
			data, err = io.ReadAll(reader)
			reader.Close()
			if err != nil {
				logErr(vm, "blob.read", err)
			}
		} else {
			// Full read
			var err error
			data, err = bucket.ReadAll(ctx, k)
			if err != nil {
				logErr(vm, "blob.read", err)
			}
		}

		if encoding == "buffer" {
			// Return native ArrayBuffer (goja converts []byte → ArrayBuffer)
			return vm.ToValue(vm.NewArrayBuffer(data))
		}
		return vm.ToValue(string(data))
	})

	// handle.write(key, content, opts?)
	handle.Set("write", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "blob.write requires (key, content)")
		}
		k := call.Arguments[0].String()
		content := call.Arguments[1].String()

		var wopts blob.WriterOptions
		ct := ""
		if len(call.Arguments) >= 3 {
			if optsObj := call.Arguments[2].ToObject(vm); optsObj != nil {
				if v := optsObj.Get("contentType"); v != nil && !goja.IsUndefined(v) {
					ct = v.String()
				}
			}
		}
		// Auto-detect content type from extension if not provided
		if ct == "" {
			ct = mime.TypeByExtension(filepath.Ext(k))
		}
		if ct != "" {
			wopts.ContentType = ct
		}

		if err := bucket.WriteAll(context.Background(), k, []byte(content), &wopts); err != nil {
			logErr(vm, "blob.write", err)
		}
		return goja.Undefined()
	})

	// handle.exists(key) → boolean
	handle.Set("exists", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "blob.exists requires a key argument")
		}
		exists, err := bucket.Exists(context.Background(), call.Arguments[0].String())
		if err != nil {
			logErr(vm, "blob.exists", err)
		}
		return vm.ToValue(exists)
	})

	// handle.stat(key) → { size, contentType, modified, etag }
	handle.Set("stat", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "blob.stat requires a key argument")
		}
		attrs, err := bucket.Attributes(context.Background(), call.Arguments[0].String())
		if err != nil {
			logErr(vm, "blob.stat", err)
		}
		obj := vm.NewObject()
		obj.Set("size", attrs.Size)
		obj.Set("contentType", attrs.ContentType)
		obj.Set("modified", attrs.ModTime.Unix())
		obj.Set("etag", attrs.ETag)
		return obj
	})

	// handle.list(prefix?, opts?) → [{ key, size, modified, isDir }]
	handle.Set("list", func(call goja.FunctionCall) goja.Value {
		prefix := ""
		if len(call.Arguments) >= 1 && !goja.IsUndefined(call.Arguments[0]) {
			prefix = call.Arguments[0].String()
		}

		delimiter := ""
		if len(call.Arguments) >= 2 {
			if optsObj := call.Arguments[1].ToObject(vm); optsObj != nil {
				if v := optsObj.Get("delimiter"); v != nil && !goja.IsUndefined(v) {
					delimiter = v.String()
				}
			}
		}

		listOpts := &blob.ListOptions{Prefix: prefix}
		if delimiter != "" {
			listOpts.Delimiter = delimiter
		}

		ctx := context.Background()
		const maxItems = 1000
		var results []interface{}

		iter := bucket.List(listOpts)
		for len(results) < maxItems {
			obj, err := iter.Next(ctx)
			if err != nil {
				break // io.EOF or other error
			}
			item := vm.NewObject()
			item.Set("key", obj.Key)
			item.Set("size", obj.Size)
			item.Set("isDir", obj.IsDir)
			if !obj.ModTime.IsZero() {
				item.Set("modified", obj.ModTime.Unix())
			}
			results = append(results, item)
		}

		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// handle.rm(key)
	handle.Set("rm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "blob.rm requires a key argument")
		}
		if err := bucket.Delete(context.Background(), call.Arguments[0].String()); err != nil {
			logErr(vm, "blob.rm", err)
		}
		return goja.Undefined()
	})

	// handle.copy(dstKey, srcKey)
	handle.Set("copy", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "blob.copy requires (dstKey, srcKey)")
		}
		dst := call.Arguments[0].String()
		src := call.Arguments[1].String()
		if err := bucket.Copy(context.Background(), dst, src, nil); err != nil {
			logErr(vm, "blob.copy", err)
		}
		return goja.Undefined()
	})

	// handle.download(remoteKey, localPath) — stream blob to workspace file
	handle.Set("download", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "blob.download requires (remoteKey, localPath)")
		}
		remoteKey := call.Arguments[0].String()
		localPath := call.Arguments[1].String()

		absPath, err := SanitizePath(workspace, localPath)
		if err != nil {
			logErr(vm, "blob.download", err)
		}

		ctx := context.Background()
		reader, err := bucket.NewReader(ctx, remoteKey, nil)
		if err != nil {
			logErr(vm, "blob.download", err)
		}
		defer reader.Close()

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			logErr(vm, "blob.download", err)
		}

		f, err := os.Create(absPath)
		if err != nil {
			logErr(vm, "blob.download", err)
		}
		defer f.Close()

		n, err := io.Copy(f, reader)
		if err != nil {
			logErr(vm, "blob.download", err)
		}

		result := vm.NewObject()
		result.Set("bytes", n)
		result.Set("file", localPath)
		return result
	})

	// handle.upload(localPath, remoteKey, opts?) — stream workspace file to blob
	handle.Set("upload", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "blob.upload requires (localPath, remoteKey)")
		}
		localPath := call.Arguments[0].String()
		remoteKey := call.Arguments[1].String()

		absPath, err := SanitizePath(workspace, localPath)
		if err != nil {
			logErr(vm, "blob.upload", err)
		}

		f, err := os.Open(absPath)
		if err != nil {
			logErr(vm, "blob.upload", err)
		}
		defer f.Close()

		// Optional content type
		var wopts blob.WriterOptions
		ct := ""
		if len(call.Arguments) >= 3 {
			if optsObj := call.Arguments[2].ToObject(vm); optsObj != nil {
				if v := optsObj.Get("contentType"); v != nil && !goja.IsUndefined(v) {
					ct = v.String()
				}
			}
		}
		if ct == "" {
			ct = mime.TypeByExtension(filepath.Ext(remoteKey))
		}
		if ct != "" {
			wopts.ContentType = ct
		}

		ctx := context.Background()
		writer, err := bucket.NewWriter(ctx, remoteKey, &wopts)
		if err != nil {
			logErr(vm, "blob.upload", err)
		}

		n, err := io.Copy(writer, f)
		if err != nil {
			writer.Close()
			logErr(vm, "blob.upload", err)
		}
		if err := writer.Close(); err != nil {
			logErr(vm, "blob.upload", err)
		}

		result := vm.NewObject()
		result.Set("bytes", n)
		return result
	})

	// handle.signedURL(key, opts?) → string
	handle.Set("signedURL", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "blob.signedURL requires a key argument")
		}
		k := call.Arguments[0].String()

		method := "GET"
		expiry := 1 * time.Hour
		if len(call.Arguments) >= 2 {
			if optsObj := call.Arguments[1].ToObject(vm); optsObj != nil {
				if v := optsObj.Get("method"); v != nil && !goja.IsUndefined(v) {
					method = strings.ToUpper(v.String())
				}
				if v := optsObj.Get("expiry"); v != nil && !goja.IsUndefined(v) {
					expiry = time.Duration(v.ToInteger()) * time.Second
				}
			}
		}

		sopts := &blob.SignedURLOptions{
			Expiry: expiry,
			Method: method,
		}
		url, err := bucket.SignedURL(context.Background(), k, sopts)
		if err != nil {
			logErr(vm, "blob.signedURL", err)
		}
		return vm.ToValue(url)
	})

	// handle.close() — optional, auto-closed by engine cleanup
	handle.Set("close", func(call goja.FunctionCall) goja.Value {
		pool.Close(key)
		return goja.Undefined()
	})

	return handle
}
