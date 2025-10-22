// Settings Page JavaScript Module

// Toggle password visibility for API keys
document.getElementById('toggleOpenaiKey')?.addEventListener('click', function() {
  const input = document.getElementById('openaiApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

document.getElementById('toggleAnthropicKey')?.addEventListener('click', function() {
  const input = document.getElementById('anthropicApiKeyInput');
  input.type = input.type === 'password' ? 'text' : 'password';
});

// Save OpenAI API Key
document.getElementById('saveOpenaiKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('openaiApiKeyInput').value.trim();

  if (!apiKey) {
    alert('Please enter an API key');
    return;
  }

  if (!apiKey.startsWith('sk-')) {
    alert('Invalid API key format. OpenAI keys start with "sk-"');
    return;
  }

  try {
    const response = await fetch('/api/api-key', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ api_key: apiKey })
    });

    if (response.ok) {
      alert('OpenAI API key saved successfully!');
      document.getElementById('openaiApiKeyInput').value = '';
    } else {
      const error = await response.text();
      alert('Failed to save API key: ' + error);
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    alert('Error saving API key: ' + error.message);
  }
});

// Save Anthropic API Key
document.getElementById('saveAnthropicKey')?.addEventListener('click', async function() {
  const apiKey = document.getElementById('anthropicApiKeyInput').value.trim();

  if (!apiKey) {
    alert('Please enter an API key');
    return;
  }

  if (!apiKey.startsWith('sk-ant-')) {
    alert('Invalid API key format. Anthropic keys start with "sk-ant-"');
    return;
  }

  try {
    // Note: You'll need to add an endpoint for Anthropic API key
    // For now, we can use the same endpoint with a different structure
    const response = await fetch('/api/settings', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        anthropic_api_key: apiKey
      })
    });

    if (response.ok) {
      alert('Anthropic API key saved successfully! Please restart the server for changes to take effect.');
      document.getElementById('anthropicApiKeyInput').value = '';
    } else {
      const error = await response.text();
      alert('Failed to save API key: ' + error);
    }
  } catch (error) {
    console.error('Error saving API key:', error);
    alert('Error saving API key: ' + error.message);
  }
});

// System Diagnostics Button
document.getElementById('systemDiagnosticsBtn')?.addEventListener('click', async function() {
  try {
    const response = await fetch('/api/updates/version');
    const data = await response.json();

    let diagnosticsInfo = `
System Diagnostics
==================
Version: ${data.version || 'Unknown'}
Status: Online
    `;

    alert(diagnosticsInfo);
  } catch (error) {
    console.error('Error fetching diagnostics:', error);
    alert('Error fetching system diagnostics');
  }
});
