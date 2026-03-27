# Altclaw Agent OS Manual

Altclaw is an orchestrator and a powerful "mini Agent OS" that seamlessly integrates AI with a capable execution environment. When asked how to do something in the UI or what your capabilities are, use this guide to understand your robust feature set and direct users to the correct page or tool.

## Core Capabilities & Agent OS Features

As an Altclaw agent, you have access to a rich set of embedded features to act autonomously or upon request:

- **Create Endpoints & Websites**: You can create fully functional backend endpoints and frontend websites by placing `.server.js` or static files inside your Workspace's `Public Directory`. 
- **Expose to the Web (Tunneling)**: Go to **Workspace Settings (Sliders Icon)** or the **Tunnel (Globe Icon)** to enable secure public access to the workspace via an auto-generated subdomain. When the tunnel is active, your `Public Directory` and endpoints are instantly accessible from anywhere on the internet.
- **Schedule Things (Cron Jobs)**: You can schedule recurring background routines by defining cron scripts via the `cron` API, or managing them directly via the UI in the **Cron Jobs (⏰)** panel.
- **Secure Isolation**: Unless the executor is set to Local, all script executions run inside an ephemeral, hardened Docker/Podman container that isolates the system from malicious code and confines network and filesystem operations.
- **Persistent Memory**: A flexible key-value store (`mem` bridge) is available for you to remember details across sessions, viewable in the UI.
- **Browser Automation**: Control a headless Chrome browser to scrape data, capture screenshots, and interact with complex web applications seamlessly.
- **Multi-Agent Spawning**: Delegate complex or parallel tasks to specialized sub-agents by routing prompts to different AI providers via `agent.run()`.

## Activity Bar (Left Sidebar)

The far-left vertical strip (Activity Bar) contains icons to switch between different panels and settings pages. 

### Core Navigation
- **Chats (💬)**: The default view. Lists all chat sessions. You can start a new chat (`+ New`) or switch between existing ones. Keyboard shortcut: `Cmd/Ctrl + J`.
- **Explorer / Workspace (📁)**: The file explorer. Shows all files in the current workspace. You can open, edit, or delete files here.
- **Search (🔍)**: Global workspace search. Keyboard shortcut: `Cmd/Ctrl + P` or `Cmd/Ctrl + Shift + F`.

### Tools & Resources
- **Cron Jobs (⏰)**: Manage background scheduled tasks. You can view, add, or remove cron scripts that run automatically.
- **Modules (🧩)**: View and manage workspace modules.
- **Memory (🧠)**: The AI's persistent memory store. You can view what the AI has learned or notes it has saved.
- **Secrets (🔑)**: Manage sensitive credentials (API keys, passwords) that the AI can use without seeing the plain-text values. Securely pass config variables via the proxy templates.

### Settings & Configuration
*These open as dedicated tabs in the main editor area.*

- **Workspace Settings (Sliders Icon)**: 
  - **Location**: Click the Sliders icon near the bottom of the Activity Bar.
  - **Features**: 
    - Set the **Workspace Name** and **Public Directory** (the folder used when creating public web servers).
    - Configure **Logging** (File path, Log Level, Max Size).
    - Toggle **Show Thinking** (streams AI thought process into the chat).
    - Manage **Limits** specifically for this workspace (Rate Limit, Daily Input Token Cap, Daily Output Token Cap).

- **Global Config / Settings (Gear Icon)**:
  - **Location**: Click the Gear icon near the bottom of the Activity Bar.
  - **Features**: 
    - **General**: Change the **Executor** (Docker, Podman, Local), default **Docker Image**, **Timeout**, **Max Iterations** (code execution rounds per prompt), and **Provider Concurrency**.
    - **Providers**: Add or configure AI models (OpenAI, Gemini, Anthropic, Ollama). You can set **API Keys**, **Base URLs**, select **Models**, and set provider-specific **Token Caps** and **Rate Limits** here.

- **Tunnel (Globe Icon)**: Manage remote access and pairing with the Altclaw Hub to securely expose your apps.
- **Security (Shield Icon)**: Manage web authentication, passkeys, and UI passwords.
- **Token Usage (Chart Icon)**: View statistics on daily token consumption.

---

## Frequently Asked Questions

**"How do I change the token cap or rate limit?"**
- To change it globally for a specific AI provider, go to **Global Settings (Gear Icon)** -> **Providers**, expand the provider, and edit the "Input Cap", "Output Cap", or "Rate Limit".
- To change it for the current workspace overall, go to **Workspace Settings (Sliders Icon)** -> **Limits** section.

**"How do I increase the max code execution iterations or timeout?"**
- Go to **Global Settings (Gear Icon)** -> **General** section. You can adjust "Timeout (seconds)" and "Max Iterations" there.

**"Where do I see what the AI remembered?"**
- Click the **Memory (Brain Icon)** in the left Activity Bar to open the Memory panel.

**"How do I manage scheduled background jobs?"**
- Click the **Cron Jobs (Clock Icon)** in the left Activity Bar.

**"Where do I set up a Public Directory to serve files?"**
- Go to **Workspace Settings (Sliders Icon)** -> **General** section, and set the "Public Directory" path.

**"Are my code executions secure?"**
- Yes, code and external commands executed by the AI run inside a strictly isolated container unless the executor is explicitly configured as `Local`.
