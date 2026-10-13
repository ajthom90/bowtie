namespace Bowtie.Core.Tests;

/// <summary>One-shot timers for deterministic start deadlines, without waiting on wall time.</summary>
internal sealed class ManualTimeProvider : TimeProvider
{
    private readonly List<Timer> _timers = new();
    private TimeSpan _elapsed;

    public override ITimer CreateTimer(TimerCallback callback, object? state, TimeSpan dueTime, TimeSpan period)
    {
        Assert.Equal(Timeout.InfiniteTimeSpan, period);
        var timer = new Timer(this, callback, state);
        lock (_timers)
        {
            _timers.Add(timer);
            timer.Change(dueTime, period);
        }
        return timer;
    }

    public void Advance(TimeSpan elapsed)
    {
        List<Timer> due;
        lock (_timers)
        {
            _elapsed += elapsed;
            due = _timers.Where(t => t.Due <= _elapsed).ToList();
            foreach (var timer in due) timer.Due = TimeSpan.MaxValue;
        }
        foreach (var timer in due) timer.Fire();
    }

    private sealed class Timer(ManualTimeProvider clock, TimerCallback callback, object? state) : ITimer
    {
        public TimeSpan Due { get; set; } = TimeSpan.MaxValue;
        private bool _disposed;

        public bool Change(TimeSpan dueTime, TimeSpan period)
        {
            lock (clock._timers)
            {
                if (_disposed) return false;
                Due = dueTime == Timeout.InfiniteTimeSpan ? TimeSpan.MaxValue : clock._elapsed + dueTime;
                return true;
            }
        }

        public void Fire() { if (!_disposed) callback(state); }
        public void Dispose()
        {
            lock (clock._timers)
            {
                _disposed = true;
                clock._timers.Remove(this);
            }
        }
        public ValueTask DisposeAsync() { Dispose(); return ValueTask.CompletedTask; }
    }
}
