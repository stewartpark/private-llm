import Cocoa

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var terminalController: TerminalWindowController?
    private var statusTimer: Timer?
    private var lastVMStatus: String = ""

    private static let statusFilePath: String = {
        NSHomeDirectory() + "/.config/private-llm/status"
    }()

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        updateIcon(running: false)

        let menu = NSMenu()
        menu.addItem(NSMenuItem(title: "Show/Hide", action: #selector(toggleWindow), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit Private LLM", action: #selector(quitApp), keyEquivalent: "q"))
        statusItem.menu = menu

        // Poll the status file every 5 seconds
        statusTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.checkVMStatus()
        }
    }

    private func checkVMStatus() {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: Self.statusFilePath)),
              let status = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
        else {
            if lastVMStatus != "" {
                lastVMStatus = ""
                updateIcon(running: false)
            }
            return
        }

        if status != lastVMStatus {
            lastVMStatus = status
            updateIcon(running: status == "RUNNING")
        }
    }

    private func updateIcon(running: Bool) {
        guard let button = statusItem.button else { return }

        let symbolName = running ? "brain.fill" : "brain"
        let image = NSImage(systemSymbolName: symbolName, accessibilityDescription: "Private LLM")

        if running {
            image?.isTemplate = false
            button.image = image
            button.contentTintColor = .systemGreen
        } else {
            image?.isTemplate = true
            button.image = image
            button.contentTintColor = nil
        }
    }

    @objc private func toggleWindow() {
        if terminalController == nil {
            terminalController = TerminalWindowController()
        }

        guard let controller = terminalController else { return }

        if controller.window?.isVisible == true {
            controller.window?.orderOut(nil)
        } else {
            controller.showWindow(nil)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
    }

    func applicationWillTerminate(_ notification: Notification) {
        statusTimer?.invalidate()
        terminalController?.terminateProcess()
        // Clean up status file
        try? FileManager.default.removeItem(atPath: Self.statusFilePath)
    }
}
