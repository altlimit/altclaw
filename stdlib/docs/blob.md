# blob — Cloud Storage Bridge

Provides a unified API for reading, writing, listing, and deleting objects in cloud storage. Supports **S3**, **GCS**, and **Azure Blob Storage** through a single consistent interface.

## blob.open(driver, bucket, opts) → handle

Opens a cloud storage bucket. Credentials are required — no auto-detection from environment.

### S3 / S3-Compatible (MinIO, R2)

```javascript
var b = blob.open("s3", "my-bucket", {
  accessKey: "{{secrets.AWS_KEY}}",
  secretKey: "{{secrets.AWS_SECRET}}",
  region: "us-east-1"
})

// S3-compatible (MinIO, Cloudflare R2, etc.)
var b = blob.open("s3", "my-bucket", {
  accessKey: "{{secrets.MINIO_KEY}}",
  secretKey: "{{secrets.MINIO_SECRET}}",
  region: "us-east-1",
  endpoint: "https://minio.example.com"
})
```

**Required:** `accessKey`, `secretKey`
**Optional:** `region` (default: "us-east-1"), `endpoint`, `sessionToken`

### Google Cloud Storage

```javascript
var b = blob.open("gs", "my-gcs-bucket", {
  credentialsJSON: "{{secrets.GCP_SA_KEY}}"
})
```

**Required:** `credentialsJSON` — base64-encoded service account JSON key (or raw JSON)

### Azure Blob Storage

```javascript
var b = blob.open("azblob", "my-container", {
  accountName: "{{secrets.AZ_ACCOUNT}}",
  accountKey: "{{secrets.AZ_KEY}}"
})
```

**Required:** `accountName`, `accountKey`

## Handle Methods

```javascript
// Read a blob → string
b.read("path/to/file.txt")

// Range read (offset + length in bytes)
b.read("large.bin", { offset: 1024, length: 512 })

// Binary data as ArrayBuffer
var buf = b.read("image.png", { encoding: "buffer" })
var bytes = new Uint8Array(buf)

// Range + buffer combined
var chunk = b.read("data.bin", { offset: 0, length: 256, encoding: "buffer" })

// Write a blob (auto-detects content type from extension)
b.write("path/to/file.txt", "content")
b.write("data.json", JSON.stringify(obj), { contentType: "application/json" })

// Check existence → boolean
b.exists("path/to/file.txt")

// Get metadata → { size, contentType, modified, etag }
b.stat("path/to/file.txt")

// List blobs → [{ key, size, modified, isDir }]
b.list("prefix/")
b.list("prefix/", { delimiter: "/" })  // directory-style

// Delete a blob
b.rm("path/to/file.txt")

// Copy within same bucket
b.copy("dest.txt", "src.txt")

// Stream large file to workspace
b.download("remote/large.zip", "local/large.zip")

// Stream workspace file to bucket
b.upload("local/file.txt", "remote/file.txt")

// Signed URL (if provider supports it)
b.signedURL("file.txt", { method: "GET", expiry: 3600 })

// Close (optional — auto-closed when script ends)
b.close()
```

## blob.transfer(srcHandle, srcKey, dstHandle, dstKey)

Cross-bucket streaming copy without temp files:

```javascript
var s3 = blob.open("s3", "source-bucket", { ... })
var gcs = blob.open("gs", "dest-bucket", { ... })
blob.transfer(s3, "data.csv", gcs, "imported/data.csv")
```

## blob.connections()

Returns an array of active bucket connection keys:

```javascript
blob.connections()  // → ["s3:my-bucket", "gs:my-gcs-bucket"]
```
