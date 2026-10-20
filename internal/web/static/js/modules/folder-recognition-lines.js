// Folder recognition lines: what Ori recognized in a folder and what its setup
// plan needs installed, from the server's facts. Home's folder card and the
// Create Workspace build pane say both lines in the same words.
//
// A classic script with no imports or exports, so the classic build pane can
// load it with a <script> tag and the ES modules can import it for its effect;
// both read window.OriFolderRecognitionLines.
(function () {
  'use strict';

  function text(value) {
    return String(value ?? '').trim();
  }

  function count(value) {
    return Math.max(0, Math.floor(Number(value) || 0));
  }

  // recognitionLine is the one line a recognized folder gets:
  //   project     "Recognized: <label> · 3 <noun> files" (the count only
  //               where the server knows it)
  //   collection  "Holds 4 projects", with options.collectionHint after it
  //   none        "Not a project Ori recognizes yet"
  // and '' for anything else.
  function recognitionLine(recognition, options = {}) {
    if (!recognition || typeof recognition !== 'object') return '';
    switch (recognition.verdict) {
      case 'project': {
        const label = text(recognition.label);
        if (!label) return '';
        const files = count(recognition.marker_count);
        const noun = text(recognition.marker_noun);
        const evidence =
          files && noun ? ` · ${files} ${noun} ${files === 1 ? 'file' : 'files'}` : '';
        return `Recognized: ${label}${evidence}`;
      }
      case 'collection': {
        const projects = count(recognition.project_count);
        if (projects < 2) return '';
        const hint = text(options.collectionHint);
        return `Holds ${projects} projects${hint ? ` · ${hint}` : ''}`;
      }
      case 'none':
        return 'Not a project Ori recognizes yet';
      default:
        return '';
    }
  }

  // needsLine is "Needs: <integration> (installed) · <provider> (install)",
  // the plugins a plan needs in the state it gives each, worded as the
  // build's host line is (sessionhttp buildNeedsLine). '' when the plan needs
  // nothing.
  function needsLine(needs) {
    const parts = (Array.isArray(needs) ? needs : [])
      .filter(need => need && typeof need === 'object' && text(need.name) && text(need.state))
      .map(need => {
        const state = text(need.state);
        return `${text(need.name)} (${state === 'ready' ? 'installed' : state})`;
      });
    return parts.length ? `Needs: ${parts.join(' · ')}` : '';
  }

  globalThis.OriFolderRecognitionLines = { recognitionLine, needsLine };
})();
