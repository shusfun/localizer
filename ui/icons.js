const SVG_NS = 'http://www.w3.org/2000/svg';
const reducedMotion = () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;

function formatPath(template, values) {
  let index = 0;
  return template.replace(/#/g, () => {
    const v = values[index++];
    return Number.isInteger(v) ? String(v) : v.toFixed(2).replace(/\.?0+$/, '');
  });
}

export function attachMorphIcon(button, spec, initialState, mountTarget = button) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 21 21');
  svg.setAttribute('aria-hidden', 'true');

  const paths = spec.templates.map(() => {
    const p = document.createElementNS(SVG_NS, 'path');
    svg.appendChild(p);
    return p;
  });
  mountTarget.replaceChildren(svg);

  let state = initialState;
  let hovered = false;
  let pressed = false;
  let rafId = 0;

  const resolvePose = () => {
    const def = spec.states[state] || spec.states[initialState];
    if (pressed && def.press) return def.press;
    if (hovered && def.hover) return def.hover;
    return def.rest;
  };

  let current = resolvePose().map(arr => [...arr]);

  const draw = coords => {
    for (let i = 0; i < paths.length; i++) {
      paths[i].setAttribute('d', formatPath(spec.templates[i], coords[i]));
    }
  };
  draw(current);

  const animateTo = target => {
    if (rafId) {
      cancelAnimationFrame(rafId);
      rafId = 0;
    }
    if (reducedMotion()) {
      current = target.map(arr => [...arr]);
      draw(current);
      return;
    }
    const start = current.map(arr => [...arr]);
    const begin = performance.now();
    const duration = 185;
    const step = now => {
      const raw = Math.min(1, (now - begin) / duration);
      const ease = 1 - Math.pow(1 - raw, 3);
      current = target.map((arr, i) => arr.map((val, j) => start[i][j] + (val - start[i][j]) * ease));
      draw(current);
      if (raw < 1) {
        rafId = requestAnimationFrame(step);
      } else {
        rafId = 0;
      }
    };
    rafId = requestAnimationFrame(step);
  };

  const sync = () => animateTo(resolvePose());
  const isDisabled = () => button.disabled || button.getAttribute('aria-disabled') === 'true';

  const onEnter = () => {
    if (isDisabled()) return;
    hovered = true;
    sync();
  };
  const onLeave = () => {
    if (!hovered && !pressed) return;
    hovered = false;
    pressed = false;
    sync();
  };
  const onDown = () => {
    if (isDisabled()) return;
    pressed = true;
    sync();
  };
  const onUp = () => {
    if (!pressed) return;
    pressed = false;
    sync();
  };

  button.addEventListener('pointerenter', onEnter);
  button.addEventListener('pointerleave', onLeave);
  button.addEventListener('focus', () => {
    if (!isDisabled() && button.matches(':focus-visible')) {
      hovered = true;
      sync();
    }
  });
  button.addEventListener('blur', onLeave);
  button.addEventListener('pointerdown', onDown);
  button.addEventListener('pointerup', onUp);
  button.addEventListener('pointercancel', onLeave);

  return {
    setState(nextState) {
      if (state === nextState) return;
      state = nextState;
      sync();
    },
    getState() {
      return state;
    },
    destroy() {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = 0;
    },
  };
}

const STATIC_PATHS = {
  search: [
    'M 9.5 3.5 C 12.81 3.5 15.5 6.19 15.5 9.5 C 15.5 12.81 12.81 15.5 9.5 15.5 C 6.19 15.5 3.5 12.81 3.5 9.5 C 3.5 6.19 6.19 3.5 9.5 3.5 Z',
    'M 13.9 13.9 L 17.7 17.7',
  ],
  devin: [
    'M 10.5 2.7 L 17.3 6.6 V 14.4 L 10.5 18.3 L 3.7 14.4 V 6.6 Z',
    'M 7.7 8.3 L 10.7 10.5 L 7.7 12.7',
    'M 11.7 12.9 H 14.1',
  ],
  agy: [
    'M 10.5 3.1 L 17.5 16.5 L 10.5 13.5 L 3.5 16.5 Z',
    'M 6.9 11.3 H 14.1',
  ],
};

export function mountStaticIcon(container, kind) {
  const defs = STATIC_PATHS[kind];
  if (!container || !defs) return;
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 21 21');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of defs) {
    const p = document.createElementNS(SVG_NS, 'path');
    p.setAttribute('d', d);
    svg.appendChild(p);
  }
  container.replaceChildren(svg);
}

export const ICON_SPECS = {
  minimize: {
    templates: ['M # # L # #'],
    states: {
      default: {
        rest: [[5.75, 10.5, 15.25, 10.5]],
        hover: [[4.75, 11.5, 16.25, 11.5]],
        press: [[6.25, 12.1, 14.75, 12.1]],
      },
    },
  },
  maximize: {
    templates: [
      'M # # L # # L # # L # # Z',
      'M # # L # # L # # L # # L # #',
    ],
    states: {
      normal: {
        rest: [
          [5.75, 5.75, 15.25, 5.75, 15.25, 15.25, 5.75, 15.25],
          [5.75, 5.75, 5.75, 5.75, 15.25, 5.75, 15.25, 15.25, 15.25, 15.25],
        ],
        hover: [
          [5.1, 5.1, 15.9, 5.1, 15.9, 15.9, 5.1, 15.9],
          [5.1, 5.1, 5.1, 5.1, 15.9, 5.1, 15.9, 15.9, 15.9, 15.9],
        ],
        press: [
          [6.3, 6.3, 14.7, 6.3, 14.7, 14.7, 6.3, 14.7],
          [6.3, 6.3, 6.3, 6.3, 14.7, 6.3, 14.7, 14.7, 14.7, 14.7],
        ],
      },
      maximized: {
        rest: [
          [5.25, 7.75, 13.25, 7.75, 13.25, 15.75, 5.25, 15.75],
          [7.75, 7.75, 7.75, 5.25, 15.75, 5.25, 15.75, 13.25, 13.25, 13.25],
        ],
        hover: [
          [4.75, 8.15, 12.85, 8.15, 12.85, 16.25, 4.75, 16.25],
          [8.15, 8.15, 8.15, 4.75, 16.25, 4.75, 16.25, 12.85, 12.85, 12.85],
        ],
        press: [
          [5.75, 8.25, 12.75, 8.25, 12.75, 15.25, 5.75, 15.25],
          [8.25, 8.25, 8.25, 5.75, 15.25, 5.75, 15.25, 12.75, 12.75, 12.75],
        ],
      },
    },
  },
  close: {
    templates: ['M # # L # #', 'M # # L # #'],
    states: {
      default: {
        rest: [[6, 6, 15, 15], [15, 6, 6, 15]],
        hover: [[5.2, 5.2, 15.8, 15.8], [15.8, 5.2, 5.2, 15.8]],
        press: [[6.6, 6.6, 14.4, 14.4], [14.4, 6.6, 6.6, 14.4]],
      },
    },
  },
  theme: {
    templates: [
      'M # # C # # # # # # C # # # # # # C # # # # # # C # # # # # # Z',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
      'M # # L # #',
    ],
    states: {
      system: {
        rest: [
          [3.9, 4.9, 8.3, 4.9, 12.7, 4.9, 17.1, 4.9, 17.1, 7.8, 17.1, 10.6, 17.1, 13.5, 12.7, 13.5, 8.3, 13.5, 3.9, 13.5, 3.9, 10.6, 3.9, 7.8, 3.9, 4.9],
          [10.5, 4.9, 10.5, 4.9],
          [17.1, 9.2, 17.1, 9.2],
          [10.5, 13.5, 10.5, 16.5],
          [7.1, 16.5, 13.9, 16.5],
          [6.9, 7.5, 9.3, 9.9],
          [17.1, 13.5, 17.1, 13.5],
          [3.9, 13.5, 3.9, 13.5],
          [3.9, 4.9, 3.9, 4.9],
        ],
        hover: [
          [3.3, 4.5, 8.1, 4.5, 12.9, 4.5, 17.7, 4.5, 17.7, 7.6, 17.7, 10.6, 17.7, 13.5, 12.9, 13.5, 8.1, 13.5, 3.3, 13.5, 3.3, 10.6, 3.3, 7.6, 3.3, 4.5],
          [10.5, 4.5, 10.5, 4.5],
          [17.7, 9, 17.7, 9],
          [10.5, 13.5, 10.5, 16.9],
          [6.5, 16.9, 14.5, 16.9],
          [6.5, 7.1, 9.9, 10.5],
          [17.7, 13.5, 17.7, 13.5],
          [3.3, 13.5, 3.3, 13.5],
          [3.3, 4.5, 3.3, 4.5],
        ],
      },
      light: {
        rest: [
          [10.5, 6.8, 12.54, 6.8, 14.2, 8.46, 14.2, 10.5, 14.2, 12.54, 12.54, 14.2, 10.5, 14.2, 8.46, 14.2, 6.8, 12.54, 6.8, 10.5, 6.8, 8.46, 8.46, 6.8, 10.5, 6.8],
          [10.5, 2.5, 10.5, 4.5],
          [16.5, 10.5, 18.5, 10.5],
          [10.5, 16.5, 10.5, 18.5],
          [2.5, 10.5, 4.5, 10.5],
          [14.74, 6.26, 16.16, 4.84],
          [14.74, 14.74, 16.16, 16.16],
          [6.26, 14.74, 4.84, 16.16],
          [6.26, 6.26, 4.84, 4.84],
        ],
        hover: [
          [10.5, 7.1, 12.38, 7.1, 13.9, 8.62, 13.9, 10.5, 13.9, 12.38, 12.38, 13.9, 10.5, 13.9, 8.62, 13.9, 7.1, 12.38, 7.1, 10.5, 7.1, 8.62, 8.62, 7.1, 10.5, 7.1],
          [10.5, 1.9, 10.5, 4.7],
          [16.3, 10.5, 19.1, 10.5],
          [10.5, 16.3, 10.5, 19.1],
          [1.9, 10.5, 4.7, 10.5],
          [14.6, 6.4, 16.58, 4.42],
          [14.6, 14.6, 16.58, 16.58],
          [6.4, 14.6, 4.42, 16.58],
          [6.4, 6.4, 4.42, 4.42],
        ],
      },
      dark: {
        rest: [
          [12.9, 3.9, 8.3, 4.3, 4.9, 7.9, 4.9, 11.9, 4.9, 15.9, 8.7, 17.9, 13.5, 16.1, 10.3, 15.1, 8.9, 12.5, 9.3, 9.5, 9.7, 7.1, 11.1, 5.1, 12.9, 3.9],
          [15.5, 4.7, 15.5, 7.3],
          [14.2, 6, 16.8, 6],
          [13.5, 16.1, 13.5, 16.1],
          [4.9, 11.9, 4.9, 11.9],
          [15.5, 6, 15.5, 6],
          [13.5, 16.1, 13.5, 16.1],
          [4.9, 11.9, 4.9, 11.9],
          [12.9, 3.9, 12.9, 3.9],
        ],
        hover: [
          [13.3, 3.5, 8.1, 4.1, 4.5, 7.7, 4.5, 12.1, 4.5, 16.3, 8.5, 18.3, 14.1, 16.3, 10.1, 15.1, 8.7, 12.5, 9.1, 9.3, 9.5, 6.9, 11.3, 4.9, 13.3, 3.5],
          [15.9, 4.1, 15.9, 7.5],
          [14.2, 5.8, 17.6, 5.8],
          [14.1, 16.3, 14.1, 16.3],
          [4.5, 12.1, 4.5, 12.1],
          [15.9, 5.8, 15.9, 5.8],
          [14.1, 16.3, 14.1, 16.3],
          [4.5, 12.1, 4.5, 12.1],
          [13.3, 3.5, 13.3, 3.5],
        ],
      },
    },
  },
  editSave: {
    templates: ['M # # L # # L # # L # # L # # L # #', 'M # # L # #'],
    states: {
      edit: {
        rest: [
          [13.5, 4.5, 16.5, 7.5, 8.5, 15.5, 4.5, 16.5, 5.5, 12.5, 13.5, 4.5],
          [11.5, 6.5, 14.5, 9.5],
        ],
        hover: [
          [13.9, 4.1, 16.9, 7.1, 8.3, 15.7, 4.1, 16.9, 5.3, 12.7, 13.9, 4.1],
          [11.9, 6.1, 14.9, 9.1],
        ],
      },
      save: {
        rest: [
          [4.7, 11.1, 8.9, 15.3, 16.5, 6.1, 16.5, 6.1, 8.9, 15.3, 4.7, 11.1],
          [8.9, 15.3, 16.5, 6.1],
        ],
        hover: [
          [4.3, 10.9, 8.9, 15.9, 17.1, 5.5, 17.1, 5.5, 8.9, 15.9, 4.3, 10.9],
          [8.9, 15.9, 17.1, 5.5],
        ],
      },
    },
  },
  keep: {
    templates: [
      'M # # L # # L # # L # # Z',
      'M # # C # # # # # #',
    ],
    states: {
      off: {
        rest: [
          [5.9, 9.9, 15.1, 9.9, 15.1, 16.5, 5.9, 16.5],
          [7.9, 9.9, 7.9, 5.3, 13.1, 4.5, 14.3, 7.3],
        ],
        hover: [
          [5.5, 9.7, 15.5, 9.7, 15.5, 16.7, 5.5, 16.7],
          [7.9, 9.7, 7.9, 4.9, 13.5, 4.1, 14.9, 6.9],
        ],
      },
      on: {
        rest: [
          [5.9, 9.9, 15.1, 9.9, 15.1, 16.5, 5.9, 16.5],
          [7.9, 9.9, 7.9, 5.1, 13.1, 5.1, 13.1, 9.9],
        ],
        hover: [
          [5.5, 9.5, 15.5, 9.5, 15.5, 16.9, 5.5, 16.9],
          [7.7, 9.5, 7.7, 4.7, 13.3, 4.7, 13.3, 9.5],
        ],
      },
    },
  },
  reset: {
    templates: [
      'M # # C # # # # # # C # # # # # #',
      'M # # L # # L # #',
    ],
    states: {
      default: {
        rest: [
          [5.7, 8.5, 7.3, 5.1, 12.3, 4.5, 15.1, 7.3, 17.3, 9.9, 15.9, 15.1, 10.9, 16.1],
          [5.3, 4.9, 5.5, 8.7, 9.3, 8.3],
        ],
        hover: [
          [5.1, 9.1, 6.9, 4.7, 12.5, 4.1, 15.5, 7.1, 17.7, 9.9, 16.1, 15.5, 10.3, 16.5],
          [4.5, 5.3, 4.9, 9.3, 8.9, 8.7],
        ],
      },
    },
  },
  cancel: {
    templates: ['M # # L # #', 'M # # L # #'],
    states: {
      default: {
        rest: [[6.5, 6.5, 14.5, 14.5], [14.5, 6.5, 6.5, 14.5]],
        hover: [[5.5, 5.5, 15.5, 15.5], [15.5, 5.5, 5.5, 15.5]],
      },
    },
  },
  chevron: {
    templates: ['M # # L # # L # #'],
    states: {
      collapsed: {
        rest: [[8.1, 6.1, 12.9, 10.5, 8.1, 14.9]],
        hover: [[8.7, 5.7, 13.7, 10.5, 8.7, 15.3]],
        press: [[8.3, 6.5, 12.5, 10.5, 8.3, 14.5]],
      },
      expanded: {
        rest: [[6.1, 8.3, 10.5, 13.1, 14.9, 8.3]],
        hover: [[5.7, 8.9, 10.5, 13.9, 15.3, 8.9]],
        press: [[6.5, 8.5, 10.5, 12.7, 14.5, 8.5]],
      },
    },
  },
  refresh: {
    templates: [
      'M # # C # # # # # # C # # # # # #',
      'M # # L # # L # #',
    ],
    states: {
      default: {
        rest: [
          [15.3, 8.5, 13.7, 5.1, 8.7, 4.5, 5.9, 7.3, 3.7, 9.9, 5.1, 15.1, 10.1, 16.1],
          [15.7, 4.9, 15.5, 8.7, 11.7, 8.3],
        ],
        hover: [
          [15.9, 9.1, 14.1, 4.7, 8.5, 4.1, 5.5, 7.1, 3.3, 9.9, 4.9, 15.5, 10.7, 16.5],
          [16.5, 5.3, 16.1, 9.3, 12.1, 8.7],
        ],
        press: [
          [14.9, 8.3, 13.3, 5.5, 8.9, 4.9, 6.3, 7.5, 4.3, 9.9, 5.5, 14.7, 9.9, 15.7],
          [15.3, 5.3, 15.1, 8.5, 11.9, 8.1],
        ],
      },
    },
  },
};
