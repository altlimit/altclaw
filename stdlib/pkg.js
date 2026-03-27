/**
 * @name pkg
 * @description Install system packages if not already present. Auto-detects package manager (apt, apk, yum, dnf, pacman, zypper).
 * @example var install = require("pkg"); install("python3", "curl", "git");
 */

// Detect package manager
function detectPM() {
  var managers = [
    { cmd: "apt-get", update: "apt-get update -qq 2>&1", install: "DEBIAN_FRONTEND=noninteractive apt-get install -y -qq" },
    { cmd: "apk", update: "apk update 2>&1", install: "apk add --no-cache" },
    { cmd: "dnf", update: "", install: "dnf install -y -q" },
    { cmd: "yum", update: "", install: "yum install -y -q" },
    { cmd: "pacman", update: "pacman -Sy --noconfirm 2>&1", install: "pacman -S --noconfirm" },
    { cmd: "zypper", update: "", install: "zypper install -y -n" }
  ];
  for (var i = 0; i < managers.length; i++) {
    var check = sys.call("sh", ["-c", "which " + managers[i].cmd]);
    if (check.exitCode === 0) return managers[i];
  }
  return null;
}

// * pkg(packages...) → {installed: string[], skipped: string[], error: string|null, pm: string}
// Checks system for requested packages and auto-installs missing ones using the local package manager.
// Supports multiple string arguments or arrays of strings.
module.exports = function () {
  var packages = [];
  for (var i = 0; i < arguments.length; i++) {
    if (typeof arguments[i] === "string") {
      packages.push(arguments[i]);
    } else if (Array.isArray(arguments[i])) {
      for (var j = 0; j < arguments[i].length; j++) {
        packages.push(arguments[i][j]);
      }
    }
  }

  var missing = [];
  var skipped = [];
  for (var k = 0; k < packages.length; k++) {
    var check = sys.call("which", [packages[k]]);
    if (check.exitCode !== 0) {
      missing.push(packages[k]);
    } else {
      skipped.push(packages[k]);
    }
  }

  if (missing.length === 0) {
    return { installed: [], skipped: skipped, error: null, pm: "" };
  }

  var pm = detectPM();
  if (!pm) {
    return { installed: [], skipped: skipped, error: "no supported package manager found (tried apt-get, apk, dnf, yum, pacman, zypper)", pm: "" };
  }

  // Update package list if needed
  if (pm.update) {
    var updateResult = sys.call("sh", ["-c", pm.update]);
    if (updateResult.exitCode !== 0) {
      return {
        installed: [],
        skipped: skipped,
        error: pm.cmd + " update failed (check internet/proxy): " + updateResult.stdout + updateResult.stderr,
        pm: pm.cmd
      };
    }
  }

  // Install missing packages
  var cmd = pm.install + " " + missing.join(" ") + " 2>&1";
  var result = sys.call("sh", ["-c", cmd]);

  if (result.exitCode !== 0) {
    return {
      installed: [],
      skipped: skipped,
      error: pm.cmd + " install failed: " + result.stdout + result.stderr,
      pm: pm.cmd
    };
  }

  return { installed: missing, skipped: skipped, error: null, pm: pm.cmd };
};