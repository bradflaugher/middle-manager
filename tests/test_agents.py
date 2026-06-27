import unittest
from unittest.mock import patch
from middle_manager.agents import autodetect_step_agents

class TestAgents(unittest.TestCase):

    @patch('middle_manager.agents.agent_available')
    def test_autodetect_step_agents_diverse(self, mock_available):
        # Mock only 'grok', 'crush', 'agy' as installed
        mock_available.side_effect = lambda name, override=None: name in ('grok', 'crush', 'agy')

        detected = autodetect_step_agents()
        self.assertEqual(detected['discover'], 'grok')
        self.assertEqual(detected['execute'], 'crush')
        self.assertEqual(detected['verify'], 'agy')
        self.assertEqual(detected['commit'], 'agy')
