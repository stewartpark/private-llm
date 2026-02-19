import Cocoa

class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private var statusItem: NSStatusItem!
    private var terminalController: TerminalWindowController?
    private var statusTimer: Timer?
    private var lastVMStatus: String = ""
    private var menu: NSMenu!

    private static let statusFilePath: String = {
        NSHomeDirectory() + "/.config/private-llm/status"
    }()

    private static let attentionStatuses: Set<String> = ["PROVISIONING", "AUTH ERROR"]

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        updateIcon(running: false)

        menu = NSMenu()
        menu.addItem(NSMenuItem(title: "Hide", action: #selector(toggleWindow), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit Private LLM", action: #selector(quitApp), keyEquivalent: "q"))
        statusItem.menu = menu

        // Set up main menu so Cmd+W works
        let mainMenu = NSMenu()
        let appMenuItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(NSMenuItem(title: "Quit Private LLM", action: #selector(quitApp), keyEquivalent: "q"))
        appMenuItem.submenu = appMenu
        mainMenu.addItem(appMenuItem)
        let fileMenuItem = NSMenuItem()
        let fileMenu = NSMenu(title: "File")
        fileMenu.addItem(NSMenuItem(title: "Close Window", action: #selector(closeWindow), keyEquivalent: "w"))
        fileMenuItem.submenu = fileMenu
        mainMenu.addItem(fileMenuItem)
        NSApp.mainMenu = mainMenu

        // Start CLI immediately (hidden) so the proxy is available right away
        terminalController = TerminalWindowController()
        terminalController?.window?.delegate = self

        let isWindowVisible = terminalController?.window?.isVisible == true
        updateActivationPolicy(windowVisible: isWindowVisible)
        updateMenuTitle(hidden: isWindowVisible)
        
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

            if Self.attentionStatuses.contains(status) {
                showWindowIfHidden()
            }
        }
    }

    private func showWindowIfHidden() {
        guard let controller = terminalController else { return }
        if controller.window?.isVisible != true {
            updateActivationPolicy(windowVisible: true)
            controller.showWindow(nil)
            NSApp.activate(ignoringOtherApps: true)
            updateMenuTitle(hidden: true)
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
        guard let controller = terminalController else { return }

        if controller.window?.isVisible == true {
            controller.window?.orderOut(nil)
            updateActivationPolicy(windowVisible: false)
            updateMenuTitle(hidden: false)
        } else {
            updateActivationPolicy(windowVisible: true)
            controller.showWindow(nil)
            NSApp.activate(ignoringOtherApps: true)
            updateMenuTitle(hidden: true)
        }
    }

    func windowDidResignKey(_ notification: Notification) {
        updateMenuTitle(hidden: false)
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        sender.orderOut(nil)
        updateActivationPolicy(windowVisible: false)
        updateMenuTitle(hidden: false)
        return false
    }

    private func updateActivationPolicy(windowVisible: Bool) {
        NSApp.setActivationPolicy(windowVisible ? .regular : .accessory)
    }

    private func updateMenuTitle(hidden: Bool) {
        let title = hidden ? "Hide" : "Show"
        for item in menu.items {
            if item.action == #selector(toggleWindow) {
                item.title = title
                break
            }
        }
    }

    @objc private func closeWindow() {
        terminalController?.window?.performClose(nil)
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
