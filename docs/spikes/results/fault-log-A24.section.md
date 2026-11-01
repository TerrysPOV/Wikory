| A24-1 | decode error propagated | RED as predicted (`{stop {PAYLOAD-CONTENT-MUST-NOT-BE-LOGGED}: code=1 out=""`) |
| A24-2 | no deadline | RED as predicted (`out=wikory: mode=full`) |
| A24-3 | the servable gate opened to any store | RED as predicted (`an unregistered checkout was served memory: wikory: mode=full \| last c`) |
| A24-4 | payload logged | RED as predicted (`relative-PAYLOAD-CONTENT-MUST-NOT-BE-LOGGED`) |
| A24-5 | synchronous discovery after the deadline | RED as predicted (`budget 300ms plus 250ms slack`) |
| A24-6 | the error log opened without `O_NONBLOCK` | RED as predicted (`the hook did not return within`) |
| A24-7 | the error log accepted when it is not a regular file | RED as predicted (`the hook wrote its error log into a FIFO`) |
| A24-8 | stdin read with no deadline | RED as predicted (`the hook did not return within`) |
