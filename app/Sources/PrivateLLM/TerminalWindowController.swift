import Cocoa
import SwiftTerm

/// Window subclass that intercepts Escape and Cmd+W to hide the window.
/// These must be caught here before SwiftTerm forwards them to the PTY.
class TerminalWindow: NSWindow {
    override func sendEvent(_ event: NSEvent) {
        if event.type == .keyDown {
            // Escape → hide window
            if event.keyCode == 53 {
                orderOut(nil)
                return
            }
            // Cmd+W → hide window
            if event.modifierFlags.contains(.command) && event.charactersIgnoringModifiers == "w" {
                orderOut(nil)
                return
            }
        }
        super.sendEvent(event)
    }
}

class TerminalWindowController: NSWindowController, NSWindowDelegate {
    private var terminalView: LocalProcessTerminalView!

    convenience init() {
        let window = TerminalWindow(
            contentRect: NSRect(x: 0, y: 0, width: 900, height: 600),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "Private LLM"
        window.isReleasedWhenClosed = false
        window.backgroundColor = .black
        window.center()
        window.setFrameAutosaveName("PrivateLLMTerminal")

        self.init(window: window)
        window.delegate = self

        setupTerminal()
    }

    /// Cmd+W and the red close button hide instead of closing
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        sender.orderOut(nil)
        return false
    }

    private func setupTerminal() {
        terminalView = LocalProcessTerminalView(frame: .zero)
        terminalView.translatesAutoresizingMaskIntoConstraints = false

        // Terminal appearance
        terminalView.nativeForegroundColor = .white
        terminalView.nativeBackgroundColor = .black

        guard let contentView = window?.contentView else { return }
        contentView.addSubview(terminalView)

        NSLayoutConstraint.activate([
            terminalView.topAnchor.constraint(equalTo: contentView.topAnchor),
            terminalView.bottomAnchor.constraint(equalTo: contentView.bottomAnchor),
            terminalView.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            terminalView.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
        ])

        terminalView.processDelegate = self
        startProcess()
    }

    private func findBinary() -> String? {
        // 1. Bundled in .app/Contents/Resources/
        if let bundled = Bundle.main.path(forResource: "private-llm", ofType: nil) {
            return bundled
        }

        // 2. Sibling to the .app bundle
        if let bundlePath = Bundle.main.bundlePath as NSString? {
            let sibling = (bundlePath.deletingLastPathComponent as NSString)
                .appendingPathComponent("private-llm")
            if FileManager.default.isExecutableFile(atPath: sibling) {
                return sibling
            }
        }

        // 3. ~/.local/bin/private-llm
        let localBin = NSHomeDirectory() + "/.local/bin/private-llm"
        if FileManager.default.isExecutableFile(atPath: localBin) {
            return localBin
        }

        // 4. Search PATH
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        task.arguments = ["private-llm"]
        let pipe = Pipe()
        task.standardOutput = pipe
        try? task.run()
        task.waitUntilExit()
        if task.terminationStatus == 0 {
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            let path = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            if let path = path, !path.isEmpty {
                return path
            }
        }

        return nil
    }

    private func startProcess() {
        guard let binaryPath = findBinary() else {
            NSLog("private-llm binary not found")

            // Show error in terminal
            let msg = "\r\n  Error: private-llm binary not found.\r\n\r\n  Searched:\r\n    1. Inside .app bundle (Contents/Resources/)\r\n    2. Next to .app bundle\r\n    3. ~/.local/bin/private-llm\r\n    4. PATH\r\n\r\n  Install with: make install\r\n"
            let terminal = terminalView.getTerminal()
            terminal.feed(text: msg)
            return
        }

        // Build environment with TERM and a full PATH for color support and tool discovery
        let shellEnv = Self.shellEnvironment()
        var env: [String] = []
        for (key, value) in shellEnv {
            if key == "TERM" { continue }
            env.append("\(key)=\(value)")
        }
        env.append("TERM=xterm-256color")

        // Check that pulumi is reachable before spawning private-llm
        if let missing = Self.findMissingDeps(["pulumi", "gcloud"], env: shellEnv) {
            let terminal = terminalView.getTerminal()
            terminal.feed(text: "\r\n  \u{1B}[1;31mMissing dependencies:\u{1B}[0m\r\n\r\n")
            for (name, hint) in missing {
                terminal.feed(text: "    \u{1B}[1m\(name)\u{1B}[0m — \(hint)\r\n")
            }
            terminal.feed(text: "\r\n  Install the missing tools and relaunch the app.\r\n")
            terminal.feed(text: "  Resolved PATH:\r\n    \(shellEnv["PATH"] ?? "(empty)")\r\n")
            return
        }

        terminalView.startProcess(
            executable: binaryPath,
            args: [],
            environment: env,
            execName: "private-llm"
        )
    }

    func terminateProcess() {
        // Send SIGTERM to all child processes of this app
        let myPid = ProcessInfo.processInfo.processIdentifier
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/pkill")
        task.arguments = ["-TERM", "-P", "\(myPid)"]
        try? task.run()
        task.waitUntilExit()
    }

    /// Launch a login shell to capture the user's full environment (PATH, etc.).
    /// macOS apps launched via launchd get a minimal env; this ensures tools like
    /// pulumi, gcloud, etc. are discoverable.
    private static func shellEnvironment() -> [String: String] {
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        let task = Process()
        task.executableURL = URL(fileURLWithPath: shell)
        task.arguments = ["-l", "-c", "env"]
        let pipe = Pipe()
        task.standardOutput = pipe
        task.standardError = FileHandle.nullDevice
        do {
            try task.run()
            task.waitUntilExit()
        } catch {
            return ProcessInfo.processInfo.environment
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        guard let output = String(data: data, encoding: .utf8) else {
            return ProcessInfo.processInfo.environment
        }

        var env: [String: String] = [:]
        for line in output.split(separator: "\n") {
            guard let eqIdx = line.firstIndex(of: "=") else { continue }
            let key = String(line[line.startIndex..<eqIdx])
            let value = String(line[line.index(after: eqIdx)...])
            env[key] = value
        }
        return env.isEmpty ? ProcessInfo.processInfo.environment : env
    }

    /// Check if required binaries are findable in the given environment's PATH.
    /// Returns nil if all found, or an array of (name, install hint) for missing ones.
    private static func findMissingDeps(_ deps: [String], env: [String: String]) -> [(String, String)]? {
        let hints: [String: String] = [
            "pulumi": "brew install pulumi",
            "gcloud": "brew install --cask google-cloud-sdk",
        ]

        let pathDirs = (env["PATH"] ?? "").split(separator: ":").map(String.init)
        var missing: [(String, String)] = []

        for dep in deps {
            let found = pathDirs.contains { dir in
                FileManager.default.isExecutableFile(atPath: (dir as NSString).appendingPathComponent(dep))
            }
            if !found {
                missing.append((dep, hints[dep] ?? "install \(dep)"))
            }
        }

        return missing.isEmpty ? nil : missing
    }

    override func showWindow(_ sender: Any?) {
        super.showWindow(sender)
        window?.makeKeyAndOrderFront(nil)
        // Ensure terminal view gets focus
        window?.makeFirstResponder(terminalView)
    }
}

extension TerminalWindowController: LocalProcessTerminalViewDelegate {
    func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}
    func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}
    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}

    func processTerminated(source: TerminalView, exitCode: Int32?) {
        DispatchQueue.main.async {
            NSApp.terminate(nil)
        }
    }
}
