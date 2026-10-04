// daily-brief-station.js — the facts every surface needs about the Daily Brief
// station in My HQ: the one link that opens it, and what the brief's state is
// called. Pure helpers only (no DOM, no network), so the station, the
// assistant drawer and the Action Center all say the same thing and
// daily-brief-station.test.js can exercise it under plain Node.

import { localDateInZone } from './home-daily-brief.js';

export const DAILY_BRIEF_STATION = 'daily-brief';

const WORKSPACE_SLUG = /^[a-z0-9][a-z0-9-]{0,79}$/;
const DAY_CODES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];

// dailyBriefStationLink is the one address that opens My HQ with the Daily
// Brief panel already open. An unusable slug yields '' so a caller hides its
// link rather than pointing somewhere else.
export function dailyBriefStationLink(slug) {
  const value = String(slug || '').trim();
  if (!WORKSPACE_SLUG.test(value)) return '';
  return `/workspaces/${value}?station=${DAILY_BRIEF_STATION}`;
}

// stationFromSearch reads `?station=` from a location.search string. Only a
// station this page knows is returned; anything else is ''.
export function stationFromSearch(search) {
  let value = '';
  try {
    value = new URLSearchParams(String(search || '')).get('station') || '';
  } catch (_) {
    return '';
  }
  return value === DAILY_BRIEF_STATION ? DAILY_BRIEF_STATION : '';
}

// isDailyBriefStationLink accepts exactly the shape dailyBriefStationLink
// builds and nothing broader: one workspace segment, one parameter.
export function isDailyBriefStationLink(href) {
  const match = /^\/workspaces\/([^/?#]+)\?station=daily-brief$/.exec(String(href || ''));
  return Boolean(match) && WORKSPACE_SLUG.test(match[1]);
}

// A clock label never breaks between its digits and its AM/PM.
function unbroken(label) {
  return String(label || '').replace(/\s/g, String.fromCharCode(160));
}

function clockIn(iso, timeZone) {
  const date = new Date(iso || '');
  if (Number.isNaN(date.getTime())) return '';
  const options = { hour: 'numeric', minute: '2-digit' };
  try {
    return unbroken(
      date.toLocaleTimeString(undefined, { ...options, timeZone: timeZone || 'UTC' })
    );
  } catch (_) {
    return unbroken(date.toLocaleTimeString(undefined, options));
  }
}

// A stored local date (YYYY-MM-DD) as "Oct 3", without shifting it through
// the viewer's time zone.
function shortDate(localDate) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(localDate || ''));
  if (!match) return String(localDate || '');
  return new Date(
    Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  ).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

// scheduleClock renders a config's 24-hour "HH:MM" as a clock label.
export function scheduleClock(scheduleTime) {
  const match = /^(\d{1,2}):(\d{2})$/.exec(String(scheduleTime || '').trim());
  if (!match) return '';
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (hour > 23 || minute > 59) return '';
  return unbroken(
    new Date(Date.UTC(2000, 0, 1, hour, minute)).toLocaleTimeString(undefined, {
      hour: 'numeric',
      minute: '2-digit',
      timeZone: 'UTC'
    })
  );
}

// nextBriefDue says when the next scheduled brief is due, in the brief's own
// time zone: "today at 8:00 AM", "tomorrow at 8:00 AM", or "Mon at 8:00 AM".
// '' when nothing is scheduled.
export function nextBriefDue(config, now) {
  if (!config || !config.schedule_enabled) return '';
  const clock = scheduleClock(config.schedule_time);
  const days = Array.isArray(config.schedule_days) ? config.schedule_days : [];
  if (!clock || !days.length) return '';
  const [dueHour, dueMinute] = String(config.schedule_time).split(':').map(Number);

  let parts;
  try {
    parts = new Intl.DateTimeFormat('en-US', {
      timeZone: config.timezone || 'UTC',
      weekday: 'short',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23'
    }).formatToParts(now || new Date());
  } catch (_) {
    return '';
  }
  const part = type => (parts.find(entry => entry.type === type) || {}).value || '';
  const today = DAY_CODES.indexOf(part('weekday').toLowerCase());
  if (today < 0) return '';
  const minutesNow = Number(part('hour')) * 60 + Number(part('minute'));

  for (let offset = 0; offset <= 7; offset++) {
    const code = DAY_CODES[(today + offset) % 7];
    if (!days.includes(code)) continue;
    if (offset === 0 && minutesNow >= dueHour * 60 + dueMinute) continue;
    if (offset === 0) return `today at ${clock}`;
    if (offset === 1) return `tomorrow at ${clock}`;
    return `${code.charAt(0).toUpperCase()}${code.slice(1)} at ${clock}`;
  }
  return '';
}

// dailyBriefStatus reduces what the server knows about the brief to one state.
//   generation — the status GET /api/personal-hq/brief/status returns
//   revision   — the current revision, or null
//   config     — the brief's config (time zone and schedule)
//   paused     — whether the assistant's check-ins are paused
export function dailyBriefStatus({ generation, revision, config, paused, now } = {}) {
  const timeZone = (config && config.timezone) || 'UTC';
  const active = String(generation || '');
  if (active === 'pending' || active === 'running') return { kind: 'preparing' };
  if (active === 'failed') return { kind: 'failed' };

  const ready = revision
    ? {
        kind: 'ready',
        today: revision.local_date === localDateInZone(timeZone, now),
        time: clockIn(revision.generated_at, timeZone),
        date: shortDate(revision.local_date)
      }
    : null;
  if (ready && ready.today) return ready;
  if (paused) return { kind: 'paused' };
  if (!config || !config.schedule_enabled) return { kind: 'not_scheduled' };
  if (ready) return ready;
  return { kind: 'due', due: nextBriefDue(config, now) };
}

// dailyBriefStationState is the station's short status: what the map
// structure and the Stations rail show, and what a screen reader hears.
export function dailyBriefStationState(status) {
  switch (status && status.kind) {
    case 'preparing':
      return {
        value: 'Preparing…',
        description: 'the Daily Brief is being prepared',
        tone: 'loading'
      };
    case 'failed':
      return {
        value: 'Failed',
        description: 'the latest Daily Brief could not be generated',
        tone: 'degraded'
      };
    case 'ready':
      return status.today
        ? {
            value: status.time ? `Ready · ${status.time}` : 'Ready',
            description: 'the Daily Brief for today is ready',
            tone: 'clear'
          }
        : {
            value: status.date ? `Ready · ${status.date}` : 'Ready',
            description: 'showing an earlier Daily Brief',
            tone: ''
          };
    case 'paused':
      return { value: 'Check-ins paused', description: 'check-ins are paused', tone: '' };
    case 'not_scheduled':
      return { value: 'Not scheduled', description: 'no Daily Brief is scheduled', tone: '' };
    default:
      return {
        value: 'No brief yet',
        description:
          status && status.due ? `first Daily Brief due ${status.due}` : 'no Daily Brief yet',
        tone: ''
      };
  }
}

// dailyBriefRowStatus is what the assistant drawer's "Today's brief" row says
// after its label: the same state as the station, in the drawer's own words.
export function dailyBriefRowStatus(status) {
  switch (status && status.kind) {
    case 'preparing':
      return 'being prepared';
    case 'failed':
      return 'couldn’t be generated';
    case 'ready': {
      const since = status.today ? status.time : status.date;
      return since ? `ready since ${since}` : 'ready';
    }
    case 'paused':
      return 'check-ins paused';
    case 'not_scheduled':
      return 'not scheduled';
    default:
      return status && status.due ? `first one due ${status.due}` : 'no brief yet';
  }
}

// briefPanelMeta is the line under the panel's heading: when the brief on
// screen was generated, and when the next one is due. An earlier brief, opened
// from the list, says only when it was generated.
export function briefPanelMeta({ revision, config, paused, earlier, now } = {}) {
  const timeZone = (config && config.timezone) || 'UTC';
  const sentences = [];
  if (revision) {
    const time = clockIn(revision.generated_at, timeZone);
    const today = revision.local_date === localDateInZone(timeZone, now);
    if (today) sentences.push(time ? `Generated at ${time}.` : 'Generated today.');
    else {
      const date = shortDate(revision.local_date);
      sentences.push(time ? `Generated ${date} at ${time}.` : `Generated ${date}.`);
    }
  }
  if (earlier) return sentences.join(' ');
  if (paused) sentences.push('Check-ins paused.');
  else {
    const due = nextBriefDue(config, now);
    sentences.push(due ? `Next brief ${due}.` : 'No brief scheduled.');
  }
  return sentences.join(' ');
}

if (typeof window !== 'undefined') {
  // Classic (non-module) scripts read the link helpers from here.
  window.OriDailyBriefStation = {
    link: dailyBriefStationLink,
    isLink: isDailyBriefStationLink,
    stationFromSearch
  };
}
