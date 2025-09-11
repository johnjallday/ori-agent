/**
 * Debug script to check what's happening with agent loading
 */

'use strict';

console.log('🔍 Debug script loaded');

// Override alert to see what errors are being shown
const originalAlert = window.alert;
window.alert = function(message) {
  console.error('🚨 ALERT:', message);
  return originalAlert.call(window, message);
};

// Check if modules are available
setTimeout(() => {
  console.log('=== Module Check ===');
  console.log('ApiClient:', typeof ApiClient);
  console.log('Logger:', typeof Logger);
  console.log('AgentManager:', typeof AgentManager);
  console.log('DolphinAgentApp:', typeof DolphinAgentApp);
  console.log('window.app:', typeof window.app);
  
  // Try to manually test the API with Promise.all like the real code
  if (typeof ApiClient !== 'undefined') {
    console.log('🌐 Testing Promise.all like the real code...');
    
    Promise.all([
      ApiClient.get('/api/agents'),
      ApiClient.get('/api/settings')
    ]).then(([agentsData, settings]) => {
      console.log('✅ Promise.all success:');
      console.log('  - agentsData:', agentsData);
      console.log('  - settings:', settings);
    }).catch(error => {
      console.error('❌ Promise.all error:', error);
    });
  }
  
  // Check if app initialized
  if (window.app && window.app.modules && window.app.modules.agents) {
    console.log('🎯 Testing agent manager refresh...');
    window.app.modules.agents.refresh()
      .then(() => {
        console.log('✅ Agent refresh successful');
      })
      .catch(error => {
        console.error('❌ Agent refresh error:', error);
      });
  }
  
}, 2000);