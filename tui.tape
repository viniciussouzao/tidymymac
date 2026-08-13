# TidyMyMac — interactive TUI demo
# Requires: tidymymac in PATH. Record with: vhs tui.tape

Output docs/images/tui-demo.gif

Set FontSize 20
Set Width 1600
Set Height 1200
Set Padding 20
Set Theme "Dracula"
Set FontFamily "CaskaydiaCove Nerd Font Mono"
Set TypingSpeed 75ms
Set Framerate 30

# Launch the TUI (dry-run: dashboard scans all categories in the background)
Type "tidymymac"
Sleep 500ms
Enter
Sleep 7s

# Select every category, then confirm to open the scan results
Type "a"
Sleep 800ms
Enter
Sleep 3s

# All scans done — open the review screen
Enter
Sleep 2s

# Scroll through the file list to show what's being reviewed
Type "jjj"
Sleep 800ms
Type "jjj"
Sleep 1.2s

# Confirm — dry-run skips the sudo warning (execute-mode only) and simulates cleaning
Enter
Sleep 2.5s

# Cleaning done — open the summary and let it sink in
Enter
Sleep 4s
