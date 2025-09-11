/**
 * Simple module loading test for debugging
 * This file can be included temporarily to verify all modules load correctly
 */

'use strict';

console.log('=== Dolphin Agent Module Test ===');

// Test utility modules
setTimeout(() => {
  const tests = [];
  
  // Test Utils
  if (typeof Utils !== 'undefined') {
    tests.push('✅ Utils module loaded');
    try {
      const testEl = Utils.createElement('div', 'test-class');
      tests.push('✅ Utils.createElement works');
    } catch (e) {
      tests.push('❌ Utils.createElement failed: ' + e.message);
    }
  } else {
    tests.push('❌ Utils module not found');
  }

  // Test Logger
  if (typeof Logger !== 'undefined') {
    tests.push('✅ Logger module loaded');
    try {
      Logger.info('Test log message');
      tests.push('✅ Logger.info works');
    } catch (e) {
      tests.push('❌ Logger.info failed: ' + e.message);
    }
  } else {
    tests.push('❌ Logger module not found');
  }

  // Test ApiClient
  if (typeof ApiClient !== 'undefined') {
    tests.push('✅ ApiClient module loaded');
  } else {
    tests.push('❌ ApiClient module not found');
  }

  // Test Manager Classes
  const managers = [
    'ThemeManager',
    'AgentManager', 
    'PluginManager',
    'ChatManager',
    'SettingsManager',
    'UpdateManager'
  ];

  managers.forEach(manager => {
    if (typeof window[manager] !== 'undefined') {
      tests.push(`✅ ${manager} module loaded`);
    } else {
      tests.push(`❌ ${manager} module not found`);
    }
  });

  // Test DolphinAgentApp
  if (typeof DolphinAgentApp !== 'undefined') {
    tests.push('✅ DolphinAgentApp module loaded');
  } else {
    tests.push('❌ DolphinAgentApp module not found');
  }

  // Test global app instance
  if (typeof window.app !== 'undefined') {
    tests.push('✅ Global app instance available');
    
    // Test app modules
    const appModules = ['theme', 'agents', 'plugins', 'chat', 'settings', 'updates'];
    appModules.forEach(module => {
      if (window.app.modules && window.app.modules[module]) {
        tests.push(`✅ App.${module} module instantiated`);
      } else {
        tests.push(`❌ App.${module} module not instantiated`);
      }
    });
  } else {
    tests.push('❌ Global app instance not found');
  }

  // Test AppState
  if (typeof window.AppState !== 'undefined') {
    tests.push('✅ AppState available');
  } else {
    tests.push('❌ AppState not found');
  }

  // Output results
  console.log('=== Module Test Results ===');
  tests.forEach(test => console.log(test));
  
  const passed = tests.filter(t => t.startsWith('✅')).length;
  const failed = tests.filter(t => t.startsWith('❌')).length;
  
  console.log(`=== Summary: ${passed} passed, ${failed} failed ===`);
  
  if (failed === 0) {
    console.log('🎉 All modules loaded successfully!');
  } else {
    console.error('⚠️  Some modules failed to load. Check the console for details.');
  }

}, 1000); // Wait 1 second for modules to initialize