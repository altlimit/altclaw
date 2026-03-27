### [ zip ] - Archive Operations

Supports both .zip and .tar.gz/.tgz formats. All paths workspace-jailed.

[ Operations ]
* zip.create(files: string[], output: string) → void
  Create archive from file/directory list. Format detected from output extension.
* zip.extract(archive: string, dest: string) → void
  Extract archive to destination directory.
* zip.list(archive: string) → [{name, size, compressed?, isDir}]
  List archive contents without extracting.
