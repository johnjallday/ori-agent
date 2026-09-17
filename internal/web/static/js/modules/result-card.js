/*
 * result-card.js — the card a parcel opens into (tasks/prd-task-run-show.md
 * FR44–FR47).
 *
 * Opening a parcel asks the server to mark it opened and returns what the run
 * produced: its title, how it ended, how long it took, a short summary or the
 * reason it failed, and what it actually paid. The card only DISPLAYS rewards;
 * it credits nothing (FR45). A reward that was zero or is unknown is left out,
 * and so is the rewards block when nothing is left.
 *
 * The card is an overlay inside the map's own viewport, not a Bootstrap modal,
 * so a guide coachmark can still point at the map behind it. Esc and Close
 * return focus to whatever opened it. With reduced motion the level bar and
 * the counts show their final values at once.
 *
 * A plain deferred script exposing window.OriResultCard, because the Home map
 * is a classic script; the Operations map (an ES module) reads the same global.
 */
(function () {
  'use strict';

  var OPEN_URL = '/api/workspace-map/parcels/';
  var COUNT_UP_MS = 900;
  var BAR_FILL_MS = 700;

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  /** "took 1m 42s", "took 42s", "took 1h 5m"; '' when unknown. */
  function formatDuration(seconds) {
    var total = Number(seconds);
    // A folder scan can finish inside a second; "took 0s" says nothing useful.
    if (!isFinite(total) || total < 1) return '';
    total = Math.round(total);
    var hours = Math.floor(total / 3600);
    var minutes = Math.floor((total % 3600) / 60);
    var secs = total % 60;
    if (hours > 0) return 'took ' + hours + 'h ' + minutes + 'm';
    if (minutes > 0) return 'took ' + minutes + 'm ' + secs + 's';
    return 'took ' + secs + 's';
  }

  function outcomeView(outcome) {
    switch (outcome) {
      case 'succeeded':
        return { label: 'Done', tone: 'done' };
      case 'partial':
        return { label: 'Partly done', tone: 'done' };
      case 'failed':
      case 'timeout':
        return { label: 'Needs a look', tone: 'attention' };
      default:
        return { label: 'Finished', tone: 'done' };
    }
  }

  function titleCase(word) {
    var text = String(word || '');
    return text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
  }

  function clamp01(value) {
    var n = Number(value);
    if (!isFinite(n)) return 0;
    return Math.max(0, Math.min(1, n));
  }

  function isFailure(parcel) {
    return parcel.outcome === 'failed' || parcel.outcome === 'timeout';
  }

  // The primary action says where it goes. A brief opens the brief and a scan
  // opens its review, whether or not it worked out.
  function primaryLabel(parcel) {
    var kind = parcel && parcel.kind;
    if (kind === 'daily_brief') return 'Open brief';
    if (kind === 'file_janitor') return 'Review files';
    return isFailure(parcel) ? 'Open task' : 'Open full result';
  }

  function stageChange(payload) {
    var xp = payload && payload.rewards && payload.rewards.xp;
    if (!xp || !xp.stage_before || !xp.stage_after || xp.stage_before === xp.stage_after) {
      return null;
    }
    return { before: xp.stage_before, after: xp.stage_after };
  }

  /**
   * The card's markup. Pure, so it can be asserted without a browser.
   * @param {object} payload the open endpoint's answer
   * @param {{workspaceName?: string, avatarHTML?: function}} context
   */
  function cardHTML(payload, context) {
    var ctx = context || {};
    var parcel = (payload && payload.parcel) || {};
    var outcome = outcomeView(parcel.outcome);
    var failed = isFailure(parcel);
    var duration = formatDuration(payload && payload.duration_seconds);
    var agent = String(parcel.agent_name || '').trim();
    var avatar =
      agent && typeof ctx.avatarHTML === 'function' ? String(ctx.avatarHTML(agent) || '') : '';

    var body = failed
      ? String((payload && payload.failure_reason) || '').trim() || 'The run did not say why.'
      : String((payload && payload.summary) || '').trim() || 'No summary from this run.';

    var html =
      '<section class="ori-result-card cozy-frame cozy-frame--lifted' +
      (failed ? ' is-attention' : '') +
      '" data-result-card role="dialog" aria-modal="false" aria-labelledby="oriResultCardTitle">' +
      '<header class="ori-result-card__head">' +
      (ctx.workspaceName
        ? '<span class="ori-result-card__workspace">' + escapeHtml(ctx.workspaceName) + '</span>'
        : '') +
      (agent
        ? '<span class="ori-result-card__agent">' +
          (avatar
            ? '<span class="ori-result-card__face" aria-hidden="true">' + avatar + '</span>'
            : '') +
          '<span class="ori-result-card__agent-name">' +
          escapeHtml(agent) +
          '</span></span>'
        : '') +
      '</header>' +
      '<h3 class="ori-result-card__title" id="oriResultCardTitle">' +
      escapeHtml(parcel.title || 'Result') +
      '</h3>' +
      '<p class="ori-result-card__meta">' +
      '<span class="ori-result-card__chip is-' +
      outcome.tone +
      '">' +
      escapeHtml(outcome.label) +
      '</span>' +
      (duration
        ? '<span class="ori-result-card__duration">' + escapeHtml(duration) + '</span>'
        : '') +
      '</p>';

    var stage = stageChange(payload);
    if (stage) {
      html +=
        '<p class="ori-result-card__stage" data-result-stage>' +
        escapeHtml((agent || 'Your agent') + ' reached ' + titleCase(stage.after) + ' stage') +
        '</p>';
    }

    html +=
      '<p class="ori-result-card__' +
      (failed ? 'reason' : 'summary') +
      '">' +
      escapeHtml(body) +
      '</p>';

    var rewards = payload && payload.rewards;
    var xp = rewards && rewards.xp && Number(rewards.xp.awarded) > 0 ? rewards.xp : null;
    var craft = rewards && Number(rewards.craft) > 0 ? Number(rewards.craft) : 0;
    if (xp || craft) {
      html += '<div class="ori-result-card__rewards" data-result-rewards>';
      if (xp) {
        var before = clamp01(xp.progress_before);
        var after = clamp01(xp.progress_after);
        var levelAfter = Number(xp.level_after) || 0;
        var levelUp = levelAfter > (Number(xp.level_before) || 0);
        html +=
          '<div class="ori-result-card__reward is-xp">' +
          '<span class="ori-result-card__reward-value" data-count-to="' +
          Number(xp.awarded) +
          '" data-count-suffix=" XP">+' +
          Number(xp.awarded) +
          ' XP</span>' +
          '<span class="ori-result-card__bar" role="progressbar" aria-label="Level ' +
          levelAfter +
          ' progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' +
          Math.round(after * 100) +
          '"><span class="ori-result-card__bar-fill" data-bar-fill data-bar-from="' +
          before +
          '" data-bar-to="' +
          after +
          '" data-bar-level-up="' +
          (levelUp ? '1' : '0') +
          '" style="transform:scaleX(' +
          after +
          ')"></span></span>' +
          '<span class="ori-result-card__level">Level ' +
          levelAfter +
          (levelUp ? ' · level up' : '') +
          '</span>' +
          '</div>';
      }
      if (craft) {
        html +=
          '<div class="ori-result-card__reward is-craft">' +
          '<span class="ori-result-card__reward-value" data-count-to="' +
          craft +
          '" data-count-suffix=" Craft">+' +
          craft +
          ' Craft</span>' +
          '</div>';
      }
      html += '</div>';
    }

    html +=
      '<div class="ori-result-card__actions">' +
      '<button type="button" class="ori-result-card__primary cozy-focusable" data-result-primary>' +
      escapeHtml(primaryLabel(parcel)) +
      '</button>' +
      '<button type="button" class="ori-result-card__close cozy-focusable" data-result-close>Close</button>' +
      '</div>' +
      '</section>';
    return html;
  }

  function prefersReducedMotion() {
    try {
      return !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
    } catch (error) {
      return false;
    }
  }

  function schedule(fn) {
    if (typeof window.requestAnimationFrame === 'function') return window.requestAnimationFrame(fn);
    return setTimeout(function () {
      fn(Date.now());
    }, 16);
  }

  // The numbers count up and the level bar moves from before to after, once.
  // With reduced motion the markup's final values simply stay (FR56).
  function animate(card) {
    if (!card || prefersReducedMotion()) return;
    // The markup already holds every final value. Nothing is rewritten until a
    // timer or frame actually runs, and the count is driven by wall-clock time,
    // so a throttled background tab can delay the motion but never leave a
    // wrong number on screen.
    Array.prototype.forEach.call(card.querySelectorAll('[data-count-to]'), function (el) {
      var target = Number(el.getAttribute('data-count-to')) || 0;
      var suffix = el.getAttribute('data-count-suffix') || '';
      var start = Date.now();
      function step() {
        var progress = Math.min(1, (Date.now() - start) / COUNT_UP_MS);
        el.textContent = '+' + Math.round(target * progress) + suffix;
        if (progress < 1) setTimeout(step, 30);
      }
      setTimeout(step, 30);
    });
    Array.prototype.forEach.call(card.querySelectorAll('[data-bar-fill]'), function (fill) {
      var from = clamp01(fill.getAttribute('data-bar-from'));
      var to = clamp01(fill.getAttribute('data-bar-to'));
      var levelUp = fill.getAttribute('data-bar-level-up') === '1';
      schedule(function () {
        fill.classList.add('is-instant');
        fill.style.transform = 'scaleX(' + from + ')';
        schedule(function () {
          fill.classList.remove('is-instant');
          if (!levelUp) {
            fill.style.transform = 'scaleX(' + to + ')';
            return;
          }
          // A level-up fills the bar, then starts the new level from empty.
          fill.style.transform = 'scaleX(1)';
          setTimeout(function () {
            fill.classList.add('is-instant');
            fill.style.transform = 'scaleX(0)';
            schedule(function () {
              fill.classList.remove('is-instant');
              fill.style.transform = 'scaleX(' + to + ')';
            });
          }, BAR_FILL_MS);
        });
      });
    });
  }

  var current = null;

  function close(options) {
    var state = current;
    if (!state) return;
    current = null;
    if (state.card && typeof state.card.removeEventListener === 'function') {
      state.card.removeEventListener('keydown', state.onKey);
    }
    if (state.host) state.host.innerHTML = '';
    var restore = !(options && options.restoreFocus === false);
    if (restore && state.origin && typeof state.origin.focus === 'function') state.origin.focus();
    if (typeof state.onClose === 'function') state.onClose();
  }

  /**
   * Open a parcel into the card.
   *
   * @param {{host: Element, parcelId: string, origin?: Element,
   *   workspaceName?: string, avatarHTML?: function,
   *   onPrimary?: function, onClose?: function, onError?: function,
   *   fetchImpl?: function}} options
   * @returns {Promise<object|null>} the payload shown, or null when nothing was.
   */
  function open(options) {
    var opts = options || {};
    var host = opts.host;
    var id = String(opts.parcelId || '').trim();
    if (!host || !id) return Promise.resolve(null);
    close({ restoreFocus: false });
    var fetchImpl = opts.fetchImpl || (typeof fetch === 'function' ? fetch : null);
    if (!fetchImpl) return Promise.resolve(null);

    return Promise.resolve(
      fetchImpl(OPEN_URL + encodeURIComponent(id) + '/open', {
        method: 'POST',
        headers: { Accept: 'application/json' }
      })
    )
      .then(function (response) {
        if (!response || !response.ok) throw new Error('HTTP ' + (response && response.status));
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !payload.parcel) throw new Error('empty card');
        host.innerHTML = cardHTML(payload, opts);
        var card = host.querySelector('[data-result-card]');
        if (!card) return null;

        var stage = stageChange(payload);
        if (stage && window.StageUpToast && typeof window.StageUpToast.markShown === 'function') {
          window.StageUpToast.markShown(payload.parcel.agent_name, stage.after);
        }

        var state = {
          host: host,
          card: card,
          origin: opts.origin || null,
          onClose: opts.onClose,
          onKey: function (event) {
            if (!event || event.key !== 'Escape') return;
            if (event.preventDefault) event.preventDefault();
            if (event.stopPropagation) event.stopPropagation();
            close();
          }
        };
        current = state;
        card.addEventListener('keydown', state.onKey);

        var closeButton = card.querySelector('[data-result-close]');
        if (closeButton) {
          closeButton.addEventListener('click', function () {
            close();
          });
        }
        var primary = card.querySelector('[data-result-primary]');
        if (primary) {
          primary.addEventListener('click', function () {
            close({ restoreFocus: false });
            if (typeof opts.onPrimary === 'function') opts.onPrimary(payload);
          });
          if (typeof primary.focus === 'function') primary.focus();
        }
        animate(card);
        return payload;
      })
      .catch(function (error) {
        console.warn('[result-card] could not open the parcel', error);
        if (typeof opts.onError === 'function') opts.onError(error);
        return null;
      });
  }

  window.OriResultCard = {
    open: open,
    close: close,
    isOpen: function () {
      return !!current;
    },
    cardHTML: cardHTML,
    formatDuration: formatDuration,
    primaryLabel: primaryLabel
  };
})();
