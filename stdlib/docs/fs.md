### [ fs ] - Filesystem (Jailed to Workspace)

[ Read & Inspect ]
* fs.read(path) → string (Read entire file)
* fs.readLines(path, start, end) → string (1-indexed, inclusive. Best for large files)
* fs.lineCount(path) → number (Count lines without loading file into memory)
* fs.list(dir) → string[] (Directory entries, filenames only)
* fs.list(dir, {detailed: true}) → [{name, isDir, size, modified}]
* fs.exists(path) → boolean
* fs.stat(path) → {size: number, isDir: boolean, modified: number (unix timestamp)}

[ Search ]
* fs.search(query) → string[] (Full-text + glob search, e.g., "**/*.js". Skips dot-prefixed dirs like .altclaw/ — use fs.list for those)
* fs.find(dir, pattern) → string[] (Recursive filename glob, e.g. fs.find('.', '*.go'). No content search)
* fs.grep(path, pattern) → [{line: string, num: number}] (Substring search within a file)
* fs.grep(path, pattern, {regex: true}) → [{line, num}] (Regex search within a file)

[ Write & Modify ]
* fs.patch(path, oldText, newText) → void
  **CRITICAL:** Prefer this over fs.write() for edits to save tokens. Fails if oldText is not perfectly unique.
* fs.patch(path, oldText, newText, {all: true}) → void (Replace ALL occurrences)
* fs.write(path, content) → void (Full overwrite. Auto-creates parent dirs)
* fs.append(path, content) → void (Appends to end of file. Auto-creates if missing)

[ File Operations ]
* fs.mkdir(path) → void (Recursive, creates parents)
* fs.copy(src, dest) → void (Auto-creates dest parent dirs)
* fs.move(src, dest) → void (Rename/Move. Auto-creates dest parent dirs)
* fs.rm(path) → void (Recursive delete)
