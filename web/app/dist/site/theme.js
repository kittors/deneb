// The website's light or dark, as the system has it, set before the page
// draws (tokens.css goes by data-theme) and changed with the system's. The
// pictures follow the system by themselves (<picture>); without scripts
// the page reads in light.
(() => {
  // Scripts run: what comes into view rises in (site.js); without them all
  // of it is there.
  document.documentElement.classList.add('js');
  // ?theme= forces one, for the stills and the recordings.
  const forced = new URLSearchParams(location.search).get('theme');
  if (forced === 'light' || forced === 'dark') {
    document.documentElement.dataset.theme = forced;
    return;
  }
  const dark = matchMedia('(prefers-color-scheme: dark)');
  const set = () => (document.documentElement.dataset.theme = dark.matches ? 'dark' : 'light');
  set();
  dark.addEventListener('change', set);
})();
