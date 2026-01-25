// Voice Input Module
// Provides push-to-talk voice input using Web Speech API and optional server transcription.

(function() {
  const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  const speechSupported = Boolean(SpeechRecognition);
  const mediaSupported = Boolean(window.MediaRecorder && navigator.mediaDevices && navigator.mediaDevices.getUserMedia);
  const storageKey = 'voiceSettings';
  const defaultSettings = {
    provider: 'auto',
    language: 'auto'
  };

  let activeSession = null;
  let warnedUnsupported = false;

  function loadSettings() {
    if (!window.localStorage) return { ...defaultSettings };
    try {
      const raw = localStorage.getItem(storageKey);
      if (!raw) return { ...defaultSettings };
      const parsed = JSON.parse(raw);
      return { ...defaultSettings, ...(parsed || {}) };
    } catch (error) {
      return { ...defaultSettings };
    }
  }

  async function loadRemoteSettings() {
    if (!window.fetch) return;
    try {
      const response = await fetch('/api/settings/speech');
      if (!response.ok) return;
      const data = await response.json();
      if (!window.localStorage) return;
      const settings = {
        provider: data.speech_provider || data.provider || defaultSettings.provider,
        language: data.speech_language || data.language || defaultSettings.language
      };
      localStorage.setItem(storageKey, JSON.stringify(settings));
    } catch (error) {
      // Ignore fetch errors; fall back to cached settings.
    }
  }

  function resolveLanguage(settings) {
    if (settings.language && settings.language !== 'auto') {
      return settings.language;
    }
    return navigator.language || 'en-US';
  }

  function notify(type, message) {
    if (window.Toast && typeof Toast[type] === 'function') {
      Toast[type](message);
      return;
    }
    alert(message);
  }

  function speechErrorMessage(error) {
    switch (error) {
      case 'not-allowed':
        return 'Microphone permission denied. Allow access to use voice input.';
      case 'service-not-allowed':
        return 'Speech recognition is blocked by the browser.';
      case 'audio-capture':
        return 'No microphone detected or audio capture failed.';
      case 'network':
        return 'Speech recognition service is unavailable.';
      case 'no-speech':
        return 'No speech detected. Try speaking louder.';
      case 'aborted':
        return 'Speech recognition was interrupted.';
      default:
        return 'Voice input error.';
    }
  }

  function getVoiceButtons() {
    return Array.from(document.querySelectorAll('[data-voice-target]'));
  }

  function setButtonRecording(button, isRecording) {
    if (!button) return;
    button.classList.toggle('is-recording', isRecording);
    button.setAttribute('aria-pressed', isRecording ? 'true' : 'false');
  }

  function setStatus(context, message, state) {
    const targets = document.querySelectorAll(`[data-voice-status=\"${context}\"]`);
    targets.forEach((el) => {
      el.textContent = message || '';
      el.classList.toggle('is-active', Boolean(message) && state !== 'error');
      el.classList.toggle('is-error', state === 'error');
    });
  }

  function clearStatus(context) {
    setStatus(context, '', '');
  }

  function normalizeTranscript(beforeText, transcript) {
    if (!transcript) return '';
    const trimmed = transcript.replace(/^\s+/, '');
    const needsSpace = beforeText && !/\s$/.test(beforeText);
    return `${needsSpace ? ' ' : ''}${trimmed}`;
  }

  function updateTargetValue(state, transcript) {
    const { target, before, after } = state;
    const insertText = normalizeTranscript(before, transcript);
    const nextValue = `${before}${insertText}${after}`;

    target.value = nextValue;
    const caretPosition = before.length + insertText.length;
    if (typeof target.setSelectionRange === 'function') {
      target.setSelectionRange(caretPosition, caretPosition);
    }

    target.dispatchEvent(new Event('input', { bubbles: true }));
  }

  function releaseAudioStream(stream) {
    if (!stream) return;
    stream.getTracks().forEach((track) => track.stop());
  }

  function finishSession(session) {
    if (!session) return;
    const { recognition, button, audioStream } = session;
    if (recognition) {
      recognition.onresult = null;
      recognition.onerror = null;
      recognition.onend = null;
    }
    releaseAudioStream(audioStream);
    setButtonRecording(button, false);
    button.disabled = false;
    clearStatus(session.context);
    if (activeSession === session) {
      activeSession = null;
    }
  }

  function stopBrowserRecognition(session) {
    if (!session || !session.recognition) return;
    try {
      session.recognition.stop();
    } catch (error) {
      // Ignore stop errors when recognition is already stopped.
    }
  }

  async function sendTranscription(session, blob) {
    if (!session) return;
    const form = new FormData();
    form.append('audio', blob, 'voice-input.webm');
    form.append('provider', session.provider);
    if (session.language && session.language !== 'auto') {
      form.append('language', session.language);
    }
    if (session.model) {
      form.append('model', session.model);
    }

    try {
      const response = await fetch('/api/transcribe', {
        method: 'POST',
        body: form
      });
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Transcription failed');
      }
      const data = await response.json();
      if (data && data.text) {
        updateTargetValue(session, String(data.text));
      }
    } catch (error) {
      notify('error', `Transcription failed: ${error.message}`);
      setStatus(session.context, 'Transcription failed.', 'error');
    } finally {
      finishSession(session);
    }
  }

  function startMediaCapture(session) {
    if (!mediaSupported) {
      notify('error', 'This browser does not support audio recording for server transcription.');
      finishSession(session);
      return;
    }

    navigator.mediaDevices.getUserMedia({ audio: true }).then((stream) => {
      if (!activeSession || activeSession !== session || session.state !== 'recording') {
        releaseAudioStream(stream);
        return;
      }

      session.audioStream = stream;
      const recorder = new MediaRecorder(stream);
      session.mediaRecorder = recorder;
      const chunks = [];

      recorder.ondataavailable = (event) => {
        if (event.data && event.data.size > 0) {
          chunks.push(event.data);
        }
      };

      recorder.onerror = () => {
        notify('error', 'Audio recording failed.');
        finishSession(session);
      };

      recorder.onstop = () => {
        const blob = new Blob(chunks, { type: recorder.mimeType || 'audio/webm' });
        sendTranscription(session, blob);
      };

      try {
        recorder.start();
      } catch (error) {
        notify('error', 'Unable to start audio recording.');
        finishSession(session);
      }
    }).catch(() => {
      notify('error', 'Microphone permission denied. Allow access to use voice input.');
      finishSession(session);
    });
  }

  function resolveProvider(settings) {
    const provider = settings.provider || 'auto';
    if (provider !== 'auto') return provider;
    if (speechSupported) return 'browser';
    if (mediaSupported) return 'openai';
    return 'off';
  }

  function stopRecording() {
    if (!activeSession) return;
    const session = activeSession;
    if (session.state === 'finalizing') return;
    session.state = 'finalizing';
    setButtonRecording(session.button, false);
    session.button.disabled = true;
    stopBrowserRecognition(session);

    if (session.provider === 'openai') {
      setStatus(session.context, 'Transcribing...', 'active');
      if (session.mediaRecorder && session.mediaRecorder.state !== 'inactive') {
        session.mediaRecorder.stop();
      } else {
        // Recording never started or already stopped; clean up immediately.
        finishSession(session);
      }
      return;
    }

    finishSession(session);
  }

  function startRecording(button) {
    const settings = loadSettings();
    const provider = resolveProvider(settings);

    if (activeSession && activeSession.state === 'finalizing') {
      notify('info', 'Finishing previous transcription. Please wait.');
      return;
    }

    if (provider === 'off') {
      notify('info', 'Voice input is turned off in Settings.');
      return;
    }

    if (provider === 'browser' && !speechSupported) {
      if (!warnedUnsupported) {
        warnedUnsupported = true;
        notify('warning', 'Voice input is not supported in this browser.');
      }
      return;
    }

    if (provider === 'openai' && !mediaSupported) {
      notify('warning', 'Server transcription is not supported in this browser.');
      return;
    }

    const targetId = button.dataset.voiceTarget;
    const target = targetId ? document.getElementById(targetId) : null;
    if (!target) {
      notify('error', 'Voice input target not found.');
      return;
    }

    stopRecording();

    const selectionStart = typeof target.selectionStart === 'number' ? target.selectionStart : target.value.length;
    const selectionEnd = typeof target.selectionEnd === 'number' ? target.selectionEnd : target.value.length;
    const before = target.value.slice(0, selectionStart);
    const after = target.value.slice(selectionEnd);

    const session = {
      button,
      target,
      before,
      after,
      provider,
      language: settings.language,
      model: settings.model || '',
      context: button.dataset.voiceContext || 'chat',
      recognition: null,
      mediaRecorder: null,
      audioStream: null,
      state: 'recording',
      mode: 'hold'
    };

    activeSession = session;
    setButtonRecording(button, true);

    if (speechSupported) {
      const recognition = new SpeechRecognition();
      recognition.interimResults = true;
      recognition.continuous = true;
      recognition.lang = resolveLanguage(settings);
      session.recognition = recognition;

      recognition.onresult = (event) => {
        if (!activeSession || activeSession !== session || session.state !== 'recording') return;
        let finalText = '';
        let interimText = '';

        for (let i = 0; i < event.results.length; i += 1) {
          const result = event.results[i];
          const transcript = result && result[0] ? result[0].transcript : '';
          if (result.isFinal) {
            finalText += transcript;
          } else {
            interimText += transcript;
          }
        }

        updateTargetValue(session, `${finalText}${interimText}`.trim());
      };

      recognition.onerror = (event) => {
        const message = speechErrorMessage(event.error);
        console.warn('Speech recognition error:', event.error);
        if (event.error && event.error !== 'no-speech') {
          notify('error', message);
        }
        setStatus(session.context, message, 'error');
      };

      recognition.onend = () => {
        if (activeSession !== session) return;
        if (session.state === 'finalizing') {
          finishSession(session);
          return;
        }
        if (session.mode === 'toggle') {
          setTimeout(() => {
            if (!activeSession || activeSession !== session || session.state !== 'recording') return;
            try {
              recognition.start();
            } catch (error) {
              finishSession(session);
            }
          }, 150);
          return;
        }
        finishSession(session);
      };

      try {
        recognition.start();
      } catch (error) {
        notify('error', 'Unable to start voice input.');
        setStatus(session.context, 'Unable to start voice input.', 'error');
        finishSession(session);
        return;
      }
    } else if (provider === 'browser') {
      notify('warning', 'Voice input is not supported in this browser.');
      setStatus(session.context, 'Voice input not supported.', 'error');
      finishSession(session);
      return;
    }

    if (provider === 'openai') {
      setStatus(session.context, 'Recording... (hold or tap to toggle)', 'active');
      startMediaCapture(session);
    } else {
      setStatus(session.context, 'Listening... (hold or tap to toggle)', 'active');
    }
  }

  function attachButtonHandlers(button) {
    button.addEventListener('pointerdown', (event) => {
      if (event.button && event.button !== 0) return;
      event.preventDefault();
      if (activeSession && activeSession.button === button && activeSession.mode === 'toggle') {
        stopRecording();
        return;
      }
      button.dataset.voiceDownAt = String(Date.now());
      if (typeof button.setPointerCapture === 'function' && event.pointerId != null) {
        try {
          button.setPointerCapture(event.pointerId);
          button.dataset.voicePointerId = String(event.pointerId);
        } catch (error) {
          // Ignore pointer capture errors.
        }
      }
      startRecording(button);
    });

    button.addEventListener('pointerup', (event) => {
      const pointerId = button.dataset.voicePointerId;
      if (pointerId && event.pointerId != null && String(event.pointerId) === pointerId) {
        if (typeof button.releasePointerCapture === 'function') {
          try {
            button.releasePointerCapture(event.pointerId);
          } catch (error) {
            // Ignore pointer capture errors.
          }
        }
        delete button.dataset.voicePointerId;
      }
      if (!activeSession || activeSession.button !== button) {
        return;
      }
      const startedAt = Number(button.dataset.voiceDownAt || 0);
      const elapsed = startedAt ? Date.now() - startedAt : 0;
      if (elapsed > 0 && elapsed < 250 && activeSession.state === 'recording') {
        activeSession.mode = 'toggle';
        setStatus(activeSession.context, 'Listening... (click to stop)', 'active');
        return;
      }
      stopRecording();
    });

    button.addEventListener('pointercancel', (event) => {
      const pointerId = button.dataset.voicePointerId;
      if (pointerId && event.pointerId != null && String(event.pointerId) === pointerId) {
        if (typeof button.releasePointerCapture === 'function') {
          try {
            button.releasePointerCapture(event.pointerId);
          } catch (error) {
            // Ignore pointer capture errors.
          }
        }
        delete button.dataset.voicePointerId;
      }
      if (activeSession && activeSession.button === button && activeSession.mode === 'toggle') {
        return;
      }
      stopRecording();
    });
  }

  function refreshButtons() {
    const settings = loadSettings();
    const buttons = getVoiceButtons();
    const provider = settings.provider || 'auto';
    const supported = (provider === 'auto' && (speechSupported || mediaSupported))
      || (provider === 'browser' && speechSupported)
      || (provider === 'openai' && mediaSupported);

    buttons.forEach((button) => {
      const disabled = settings.provider === 'off' || !supported;
      button.disabled = disabled;
      if (disabled) {
        button.classList.remove('is-recording');
        button.setAttribute('aria-pressed', 'false');
        if (settings.provider === 'off') {
          button.title = 'Voice input is turned off in Settings';
        } else if (provider === 'openai' && !mediaSupported) {
          button.title = 'Server transcription is not supported in this browser';
        } else {
          button.title = 'Voice input is not supported in this browser';
        }
      } else {
        button.title = 'Hold to talk';
      }
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    refreshButtons();
    getVoiceButtons().forEach(attachButtonHandlers);
    loadRemoteSettings().then(() => {
      refreshButtons();
    });
  });

  window.addEventListener('voice-settings:updated', () => {
    refreshButtons();
  });
})();
