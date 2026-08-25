const cleaners = [
  { name: 'Temporary Files', target: '/tmp · /var/tmp · user temp', level: 'safe', sudo: true },
  { name: 'Application Caches', target: '~/Library/Caches', level: 'safe', sudo: false },
  { name: 'System Logs', target: '~/Library/Logs · /var/log', level: 'safe', sudo: true },
  { name: 'Homebrew Cache', target: 'packages cached by brew', level: 'safe', sudo: false },
  { name: 'Docker Artifacts', target: 'containers · images · volumes', level: 'safe', sudo: false },
  { name: 'iOS Backups', target: '~/Library/…/MobileSync/Backup', level: 'caution', sudo: false },
  { name: 'macOS Updates', target: 'old update residues & installers', level: 'caution', sudo: true },
  { name: 'Downloads', target: '.dmg · .pkg · large items', level: 'caution', sudo: false },
  { name: 'App Orphans', target: 'leftovers of uninstalled apps', level: 'caution', sudo: false },
  { name: 'Xcode', target: 'DerivedData · archives · simulators', level: 'caution', sudo: false },
  { name: 'Development Artifacts', target: 'Go build & module cache', level: 'safe', sudo: false },
  { name: 'Project Artifacts', target: 'node_modules · dist · target · .venv', level: 'safe', sudo: false },
  { name: 'Time Machine Snapshots', target: 'local snapshots on disk', level: 'caution', sudo: true },
  { name: 'Trash', target: 'files awaiting permanent removal', level: 'safe', sudo: false },
];

const grid = document.getElementById('cleaner-grid');
grid.innerHTML = cleaners.map((cleaner, index) => `
  <article class="reveal card rounded-xl p-5" data-delay="${index % 3}">
    <div class="flex items-start justify-between gap-3 mb-2">
      <h3 class="font-mono font-bold text-[14px] leading-snug">${cleaner.name}</h3>
      <span class="badge badge-${cleaner.level}">${cleaner.level === 'safe' ? 'SAFE' : 'CAUTION'}</span>
    </div>
    <p class="font-mono text-[12px] text-fog leading-relaxed">${cleaner.target}</p>
    ${cleaner.sudo ? '<span class="badge badge-sudo mt-3 inline-block">sudo</span>' : ''}
  </article>
`).join('');

const motionPreference = window.matchMedia('(prefers-reduced-motion: reduce)');
const noAnim = new URLSearchParams(location.search).has('noanim');
const revealEls = document.querySelectorAll('.reveal');
if (motionPreference.matches || noAnim || !('IntersectionObserver' in window)) {
  revealEls.forEach((element) => element.classList.add('is-visible'));
} else {
  const observer = new IntersectionObserver((entries) => {
    entries.forEach((entry) => {
      if (entry.isIntersecting) {
        entry.target.classList.add('is-visible');
        observer.unobserve(entry.target);
      }
    });
  }, { threshold: 0.12, rootMargin: '0px 0px -8% 0px' });
  revealEls.forEach((element) => observer.observe(element));
}

const nav = document.getElementById('nav');
const updateNav = () => nav.classList.toggle('scrolled', window.scrollY > 8);
updateNav();
window.addEventListener('scroll', updateNav, { passive: true });

const syncDemoVideos = () => {
  document.querySelectorAll('[role="tabpanel"] video').forEach((video) => {
    const panel = video.closest('[role="tabpanel"]');
    if (motionPreference.matches || panel.hidden) {
      video.pause();
      return;
    }
    video.play().catch(() => {});
  });
};

const activateTab = (tabs, activeTab, syncVideos = false) => {
  tabs.forEach((tab) => {
    const selected = tab === activeTab;
    tab.setAttribute('aria-selected', String(selected));
    tab.tabIndex = selected ? 0 : -1;
    const panel = document.getElementById(tab.getAttribute('aria-controls'));
    if (panel) panel.hidden = !selected;
  });
  if (syncVideos) syncDemoVideos();
};

document.querySelectorAll('[role="tablist"]').forEach((tablist) => {
  const tabs = [...tablist.querySelectorAll('[role="tab"]')];
  const controlsDemo = tabs.some((tab) => tab.getAttribute('aria-controls') === 'panel-tui');
  tabs.forEach((tab) => {
    tab.addEventListener('click', () => activateTab(tabs, tab, controlsDemo));
    tab.addEventListener('keydown', (event) => {
      const currentIndex = tabs.indexOf(tab);
      let nextIndex;
      if (event.key === 'ArrowRight') nextIndex = (currentIndex + 1) % tabs.length;
      if (event.key === 'ArrowLeft') nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
      if (event.key === 'Home') nextIndex = 0;
      if (event.key === 'End') nextIndex = tabs.length - 1;
      if (nextIndex === undefined) return;
      event.preventDefault();
      activateTab(tabs, tabs[nextIndex], controlsDemo);
      tabs[nextIndex].focus();
    });
  });
});

motionPreference.addEventListener('change', syncDemoVideos);
syncDemoVideos();

const copyText = async (text) => {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fall through to the legacy clipboard path.
    }
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  let copied = false;
  try {
    copied = document.execCommand('copy');
  } catch {
    copied = false;
  }
  textarea.remove();
  return copied;
};

document.querySelectorAll('.copy-btn').forEach((button) => {
  button.addEventListener('click', async () => {
    const target = document.getElementById(button.dataset.copyTarget);
    let text;
    if (button.dataset.copyTarget === 'install-code') {
      const panel = document.querySelector('[data-install-panel]:not([hidden])');
      const line = panel && panel.querySelector('.install-line');
      text = line ? line.textContent : '';
    } else {
      text = target ? target.textContent : '';
    }
    text = text.replace(/^\s*\$\s*/, '').trim();

    const previousLabel = button.textContent;
    const copied = text !== '' && await copyText(text);
    button.classList.toggle('copied', copied);
    button.textContent = copied ? 'copied ✓' : 'copy failed';
    window.setTimeout(() => {
      button.classList.remove('copied');
      button.textContent = previousLabel;
    }, 1600);
  });
});
