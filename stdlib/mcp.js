/**
 * @name mcp
 * @description MCP client — connect to external MCP servers via stdio or HTTP
 * @example var client = require("mcp"); var c = client.connect({url: "http://example.com/mcp"});
 */

// JSON-RPC 2.0 helper
var nextId = 1;
function jsonrpc(method, params) {
  return JSON.stringify({
    jsonrpc: "2.0",
    id: nextId++,
    method: method,
    params: params || {}
  });
}

function parseResponse(text) {
  var resp = JSON.parse(text);
  if (resp.error) {
    throw new Error("MCP error " + resp.error.code + ": " + resp.error.message);
  }
  return resp.result;
}

// HTTP transport — uses fetch() to POST JSON-RPC 2.0
function HttpTransport(url) {
  this.url = url;
  this.headers = { "Content-Type": "application/json" };
}

HttpTransport.prototype.send = function (method, params) {
  var body = jsonrpc(method, params);
  var resp = fetch(this.url, {
    method: "POST",
    body: body,
    headers: this.headers
  });
  if (resp.status !== 200 && resp.status !== 204) {
    throw new Error("MCP HTTP error: " + resp.status + " " + resp.statusText);
  }
  if (resp.status === 204) return null;
  return parseResponse(resp.text());
};

HttpTransport.prototype.close = function () {
  // Nothing to clean up for HTTP
};

// Stdio transport — spawns a process via sys.popen and communicates via stdin/stdout
function StdioTransport(command, args, image) {
  // Switch Docker image if specified
  if (image) {
    sys.setImage(image, {
      build: "mcp.Dockerfile",
      volumes: [
        "altclaw-pkg-cache:/root/.npm",
        "altclaw-npm-global:/opt/npm-global",
        "altclaw-cache:/root/.cache"
      ]
    });
  }

  // npx internally pipes stdout which causes block buffering — the response
  // never reaches readLine. Instead, pre-install the package globally so the
  // binary ends up on PATH, then popen the binary directly.
  if (command === "npx") {
    // Extract the package name from args (skip flags like -y)
    var pkg = null;
    var cmdArgs = [];
    var pastPkg = false;
    for (var i = 0; i < (args || []).length; i++) {
      if (!pastPkg && args[i].charAt(0) === "-") continue; // skip flags
      if (!pastPkg) {
        pkg = args[i];
        pastPkg = true;
      } else {
        cmdArgs.push(args[i]);
      }
    }
    if (pkg) {
      // Install globally so the binary is on PATH (skips if already installed via volume)
      ui.log("📦 Installing " + pkg + " (first run may take a moment)...");
      sys.call("npm", ["install", "-g", pkg]);
      // Resolve the binary name from the global install (NPM_CONFIG_PREFIX=/opt/npm-global)
      var binResult = sys.call("bash", ["-c",
        "node -e \"var b=require('/opt/npm-global/lib/node_modules/" + pkg + "/package.json').bin||{};" +
        "console.log(typeof b==='string'?'" + pkg.split("/").pop() + "':Object.keys(b)[0]||'" + pkg.split("/").pop() + "')\""
      ]);
      var binName = (binResult.stdout || "").trim();
      if (!binName) binName = pkg.split("/").pop();
      this._handle = sys.popen(binName, cmdArgs);
    } else {
      this._handle = sys.popen(command, args || []);
    }
  } else {
    this._handle = sys.popen(command, args || []);
  }

  // Give the server a moment to start
  sleep(2000);
}

StdioTransport.prototype.send = function (method, params) {
  var body = jsonrpc(method, params) + "\n";
  sys.write(this._handle, body);

  // Read the response line (blocks until available)
  var respLine = sys.readLine(this._handle, 30000);
  return parseResponse(respLine);
};

StdioTransport.prototype.close = function () {
  if (this._handle) {
    try { sys.terminate(this._handle); } catch (e) { }
  }
};

// MCP Client
function Client(transport) {
  this._transport = transport;
  this._initialized = false;
}

// initialize performs the MCP handshake
Client.prototype._init = function () {
  if (this._initialized) return;

  var result = this._transport.send("initialize", {
    protocolVersion: "2025-03-26",
    capabilities: {},
    clientInfo: { name: "altclaw", version: "0.1.0" }
  });

  this._serverInfo = result.serverInfo || {};
  this._capabilities = result.capabilities || {};

  // Send initialized notification (no id = notification)
  try {
    var body = JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" });
    if (this._transport.url) {
      fetch(this._transport.url, { method: "POST", body: body, headers: { "Content-Type": "application/json" } });
    } else if (this._transport._handle) {
      sys.write(this._transport._handle, body + "\n");
    }
  } catch (e) { }

  this._initialized = true;
};

// tools returns the list of available tools
// tools() → [{name, description, inputSchema}]
Client.prototype.tools = function () {
  this._init();
  var result = this._transport.send("tools/list", {});
  return result.tools || [];
};

// call invokes a tool by name with arguments
// call(name, args) → string (text content from result)
Client.prototype.call = function (name, args) {
  this._init();
  var result = this._transport.send("tools/call", {
    name: name,
    arguments: args || {}
  });

  // Extract text content from MCP response
  if (result.content && result.content.length > 0) {
    return result.content.map(function (c) { return c.text || ""; }).join("\n");
  }
  return JSON.stringify(result);
};

// close disconnects from the server
Client.prototype.close = function () {
  this._transport.close();
};

// connect creates a new MCP client
// connect({url: "http://..."}) — HTTP transport
// connect({command: "npx", args: [...], image: "altclaw/mcp"}) — stdio transport
module.exports = {
  // connect(opts) → Client
  connect: function (opts) {
    var transport;
    if (opts.url) {
      transport = new HttpTransport(opts.url);
    } else if (opts.command) {
      transport = new StdioTransport(opts.command, opts.args || [], opts.image || "altclaw/mcp");
    } else {
      throw new Error("mcp.connect requires either 'url' or 'command'");
    }
    return new Client(transport);
  }
};
