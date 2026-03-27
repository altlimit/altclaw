### [ csv ] - CSV Reader / Writer
Read and write CSV files. Supports streaming reads for large files and customizable delimiters.
All paths are jailed to the workspace directory.

[ Reading ]
* csv.read(path: string, opts?: object, callback?: (row) => boolean|void) → object[] | string[][] | number
  - Without callback: loads all rows and returns them
  - With callback: streams rows one-by-one, returns count of rows processed
  - Return false from callback to stop early
  - With header (default): rows are objects with column-name keys
  - Without header: rows are arrays of strings

  Options:
    header    (default true)  — first row is column names
    delimiter (default ",")   — field separator
    skip      (default 0)     — data rows to skip after header
    comment   (default "")    — ignore lines starting with this character

  Examples:
    // Load all rows as objects
    var rows = csv.read("./data.csv");
    // → [{name: "Alice", age: "30"}, {name: "Bob", age: "25"}]

    // Stream large files row-by-row
    csv.read("./huge.csv", function(row) {
      output(row.name);
    });

    // Tab-delimited, no header
    csv.read("./data.tsv", {delimiter: "\t", header: false}, function(row) {
      // row is ["Alice", "30"]
    });

    // Stop early
    csv.read("./logs.csv", function(row) {
      if (row.id > 1000) return false;
    });

[ Writing ]
* csv.write(path: string, rows: object[] | string[][], opts?: object) → void
  - Writes a CSV file (creates or overwrites)
  - Object rows: column names auto-derived from keys (sorted), header row written
  - Array rows: written as-is, no header unless columns option provided

  Options:
    delimiter (default ",")  — field separator
    columns   (auto)         — explicit column names (required for array rows to get a header)

  Examples:
    csv.write("./output.csv", [
      {name: "Alice", age: 30},
      {name: "Bob", age: 25}
    ]);

    csv.write("./output.tsv", [["Alice", 30], ["Bob", 25]], {
      delimiter: "\t",
      columns: ["name", "age"]
    });

[ Appending ]
* csv.append(path: string, rows: object[] | string[][], opts?: object) → void
  - Appends rows to an existing CSV file (no header re-written)
  - For object rows, reads existing header to maintain column order

  Examples:
    csv.append("./output.csv", [{name: "Charlie", age: 35}]);
    csv.append("./output.csv", [["Charlie", 35]]);
