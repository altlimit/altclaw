### [ cron ] - Task Scheduling

* cron.add(schedule: string, content: string, opts?: {script: boolean}) → string (id)
  - schedule: Cron expression for repeating ('*/5 * * * *'), OR one-shot: duration ('10s','5m','2h') for a delayed run, or datetime ('2026-03-15 09:00') for a specific time.
    Durations and datetimes run once then auto-remove. Cron expressions repeat on schedule.
  - content: Instructions for AI, OR script content if opts.script=true.
  - opts.script: If true, content must be a .js filepath OR inline module.exports code.
    Example (File): cron.add('5m', '.altclaw/task.js', {script: true})
    Example (Inline): cron.add('5m', 'module.exports = function() { ui.log("hi"); }', {script: true})

* cron.rm(id: string) → void
* cron.list() → [{id, schedule, instructions, one_shot, created_at, next_run}]
