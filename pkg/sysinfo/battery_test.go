package sysinfo

import (
	"context"
	"testing"
)

const validBatteryFixture = `+-o AppleSmartBattery  <class AppleSmartBattery, id 0x100000123, registered, matched, active, busy 0 (2 retries), last matched 2>
    {
      "CurrentCapacity" = 87
      "MaxCapacity" = 100
      "CycleCount" = 342
      "IsCharging" = No
    }
`

const noBatteryFixture = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 15>
    {
      "IOClass" = "IORegistryEntry"
    }
`

func TestParseBatteryIoreg(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantLaptop bool
		wantPct    int
		wantPctOK  bool
		wantCycles int
		wantCycOK  bool
	}{
		{
			name:       "valid laptop output",
			raw:        validBatteryFixture,
			wantLaptop: true,
			wantPct:    87,
			wantPctOK:  true,
			wantCycles: 342,
			wantCycOK:  true,
		},
		{
			name:       "no AppleSmartBattery block (desktop)",
			raw:        noBatteryFixture,
			wantLaptop: false,
			wantPctOK:  false,
			wantCycOK:  false,
		},
		{
			name:       "truncated output",
			raw:        `+-o AppleSmartBattery  <class AppleSmartBattery, id 0x1, registered> { "Curr`,
			wantLaptop: true,
			wantPctOK:  false,
			wantCycOK:  false,
		},
		{
			name: "missing CycleCount key",
			raw: `+-o AppleSmartBattery <class AppleSmartBattery>
    {
      "CurrentCapacity" = 50
      "MaxCapacity" = 100
    }`,
			wantLaptop: true,
			wantPct:    50,
			wantPctOK:  true,
			wantCycOK:  false,
		},
		{
			name: "non-numeric capacity",
			raw: `+-o AppleSmartBattery <class AppleSmartBattery>
    {
      "CurrentCapacity" = "unknown"
      "MaxCapacity" = 100
      "CycleCount" = 10
    }`,
			wantLaptop: true,
			wantPctOK:  false,
			wantCycles: 10,
			wantCycOK:  true,
		},
		{
			name: "MaxCapacity of 0 avoids div-by-zero",
			raw: `+-o AppleSmartBattery <class AppleSmartBattery>
    {
      "CurrentCapacity" = 0
      "MaxCapacity" = 0
      "CycleCount" = 5
    }`,
			wantLaptop: true,
			wantPctOK:  false,
			wantCycles: 5,
			wantCycOK:  true,
		},
		{
			name: "out-of-range cycle count",
			raw: `+-o AppleSmartBattery <class AppleSmartBattery>
    {
      "CurrentCapacity" = 50
      "MaxCapacity" = 100
      "CycleCount" = -5
    }`,
			wantLaptop: true,
			wantPct:    50,
			wantPctOK:  true,
			wantCycOK:  false,
		},
		{
			name:       "empty output",
			raw:        "",
			wantLaptop: false,
			wantPctOK:  false,
			wantCycOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBatteryIoreg(tt.raw)

			if got.isLaptop != tt.wantLaptop {
				t.Errorf("isLaptop = %v, want %v", got.isLaptop, tt.wantLaptop)
			}
			if got.percentKnown != tt.wantPctOK {
				t.Errorf("percentKnown = %v, want %v", got.percentKnown, tt.wantPctOK)
			}
			if tt.wantPctOK && got.percent != tt.wantPct {
				t.Errorf("percent = %d, want %d", got.percent, tt.wantPct)
			}
			if got.cyclesKnown != tt.wantCycOK {
				t.Errorf("cyclesKnown = %v, want %v", got.cyclesKnown, tt.wantCycOK)
			}
			if tt.wantCycOK && got.cycles != tt.wantCycles {
				t.Errorf("cycles = %d, want %d", got.cycles, tt.wantCycles)
			}
		})
	}
}

func TestGatherBatteryMissingBinary(t *testing.T) {
	orig := ioregPath
	ioregPath = "/nonexistent/ioreg"
	defer func() { ioregPath = orig }()

	got := gatherBattery(context.Background())
	if got.isLaptop || got.hostTypeKnown || got.percentKnown || got.cyclesKnown {
		t.Errorf("expected all-unknown battery info on missing binary, got %+v", got)
	}
}
