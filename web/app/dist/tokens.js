// Generated from web/tokens.json (go run ./web/tokens/gen).
export default {
  "color": {
    "black": "#000000",
    "dark": {
      "ballHi": "#6a6a73",
      "ballLo": "#2b2b30",
      "ballMid": "#44444b",
      "bg": "#111113",
      "dots": "rgba(255, 255, 255, 0.34)",
      "ink": "#fafafa",
      "ink2": "#a1a1aa",
      "ink3": "#6b6b74",
      "line": "rgba(255, 255, 255, 0.07)",
      "line2": "rgba(255, 255, 255, 0.1)",
      "ok": "#34d399",
      "primary": "#4c82ff",
      "primary2": "#3b6ff0",
      "shadow": "rgba(0, 0, 0, 0.5)",
      "track": "rgba(255, 255, 255, 0.085)",
      "well": "#19191c",
      "well2": "#222226"
    },
    "light": {
      "ballHi": "#ffffff",
      "ballLo": "#d6d8de",
      "ballMid": "#f1f2f4",
      "bg": "#ffffff",
      "dots": "rgba(9, 9, 11, 0.32)",
      "ink": "#18181b",
      "ink2": "#71717a",
      "ink3": "#a1a1aa",
      "line": "rgba(9, 9, 11, 0.07)",
      "line2": "rgba(9, 9, 11, 0.1)",
      "ok": "#22c55e",
      "primary": "#2f6bff",
      "primary2": "#2457e0",
      "shadow": "rgba(9, 9, 11, 0.09)",
      "track": "rgba(9, 9, 11, 0.09)",
      "well": "#f4f4f6",
      "well2": "#ececef"
    },
    "owners": [
      "#3b82f6",
      "#ec4899",
      "#f59e0b",
      "#8b5cf6",
      "#22c55e",
      "#06b6d4"
    ],
    "white": "#ffffff"
  },
  "desktop": {
    "allview.go": {
      "regionBottom": 18,
      "regionIn": 420,
      "regionReach": 72,
      "regionSide": 28,
      "regionTop": 44
    },
    "canvas.go": {
      "cardWidth": 300,
      "glide": {
        "spring": {
          "duration": 420
        }
      },
      "liftPose": {
        "rotate": -3,
        "scale": 1.06,
        "y": -4
      },
      "nameWidth": 128,
      "relayFade": 280,
      "slotGap": 10,
      "slotTile": 40,
      "soft": {
        "spring": {
          "duration": 360
        }
      },
      "stepAside": {
        "spring": {
          "duration": 260
        }
      },
      "stepBack": {
        "delay": 140,
        "spring": {
          "duration": 320
        }
      },
      "tileSize": 60,
      "trackIn": 480,
      "trackOut": 280,
      "trayBand": 104
    },
    "links.go": {
      "beamSpeed": 150,
      "beamTail": 74,
      "closedRun": 1100,
      "closedSay": 4000,
      "relayHold": 0.2,
      "waitFade": 240
    },
    "menubar.go": {
      "panelMaxHeight": 560,
      "panelWidth": 320
    },
    "model.go": {
      "offlineGrey": "#a1a1aa"
    },
    "panels.go": {
      "cardNameWidth": 186
    },
    "view.go": {
      "headerHeight": 76,
      "lanNameWidth": 420,
      "sidebarWidth": 232
    },
    "views.go": {
      "inviteSpring": {
        "spring": {
          "bounce": 0.2,
          "duration": 300
        }
      }
    }
  },
  "generatedBy": "go run ./web/tokens/gen, from cmd/deneb and MyGo's ui package. Do not edit by hand.",
  "spring": {
    "Bouncy": {
      "bounce": 0.3,
      "duration": 500
    },
    "Smooth": {
      "bounce": 0,
      "duration": 500
    },
    "Snappy": {
      "bounce": 0.15,
      "duration": 500
    }
  },
  "theme": {
    "fontSize": 13
  }
};
