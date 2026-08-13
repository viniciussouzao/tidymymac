# TidyMyMac — CLI demo
# Requires: tidymymac in PATH. Record with: vhs cli.tape

Output docs/images/cli-demo.gif

Set FontSize 20
Set Width 1600
Set Height 1000
Set Padding 20
Set Theme "Dracula"
Set FontFamily "CaskaydiaCove Nerd Font Mono"
Set TypingSpeed 75ms
Set Framerate 30

# Scan a few categories (read-only, dry-run by default)
Type "tidymymac scan trash downloads homebrew"
Sleep 400ms
Enter
Sleep 4s

# Storage stats
Type "tidymymac stats"
Sleep 400ms
Enter
Sleep 2.5s

# Explain what macOS hides under "System Data"
Type "tidymymac explain system-data"
Sleep 400ms
Enter
Sleep 4s
