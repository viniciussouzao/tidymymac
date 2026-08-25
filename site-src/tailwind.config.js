/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./site/index.html', './site/assets/site.js'],
  theme: {
    extend: {
      screens: {
        xs: '375px',
      },
      colors: {
        ink: '#16151A',
        panel: '#1E1C24',
        panel2: '#252231',
        coral: '#FF6B6B',
        gold: '#F59E0B',
        mint: '#10B981',
        grape: '#7C3AED',
        ruby: '#EF4444',
        lagoon: '#00B881',
        fog: '#888888',
        mist: '#AAAAAA',
        smoke: '#626262',
      },
      fontFamily: {
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
    },
  },
};
