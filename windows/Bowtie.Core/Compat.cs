// .NET Standard 2.0 (the UWP Xbox app) lacks a few APIs Bowtie.Core uses on
// .NET 8. These fill the gaps so call sites read the same on both targets.
// PolySharp supplies the compiler-only types (IsExternalInit, Index/Range,
// nullable attributes).

namespace Bowtie.Core
{
    /// <summary>Static BCL APIs that .NET Standard 2.0 lacks (static members can't be extensions).</summary>
    internal static class Shim
    {
#if NETSTANDARD2_0
        public static bool IsFinite(double d) => !double.IsNaN(d) && !double.IsInfinity(d);

        public static double Clamp(double value, double min, double max) =>
            value < min ? min : value > max ? max : value;

        public static int Clamp(int value, int min, int max) =>
            value < min ? min : value > max ? max : value;

        public static T[] EnumValues<T>() where T : struct, Enum => (T[])Enum.GetValues(typeof(T));

        public static void MoveOverwrite(string from, string to)
        {
            if (File.Exists(to)) File.Delete(to);
            File.Move(from, to);
        }
#else
        public static bool IsFinite(double d) => double.IsFinite(d);

        public static double Clamp(double value, double min, double max) => Math.Clamp(value, min, max);

        public static int Clamp(int value, int min, int max) => Math.Clamp(value, min, max);

        public static T[] EnumValues<T>() where T : struct, Enum => Enum.GetValues<T>();

        public static void MoveOverwrite(string from, string to) => File.Move(from, to, overwrite: true);
#endif
    }
}

#if NETSTANDARD2_0
namespace Bowtie.Core
{
    internal static class NetStandardCompat
    {
        public static bool Contains(this string s, string value, StringComparison comparison) =>
            s.IndexOf(value, comparison) >= 0;

        public static bool EndsWith(this string s, char value) =>
            s.Length > 0 && s[s.Length - 1] == value;

        public static async Task<string> ReadAsStringAsync(this HttpContent content, CancellationToken ct)
        {
            ct.ThrowIfCancellationRequested();
            return await content.ReadAsStringAsync().ConfigureAwait(false);
        }
    }
}

namespace System.Threading.Tasks
{
    /// <summary>The non-generic TaskCompletionSource of .NET 5+.</summary>
    internal sealed class TaskCompletionSource
    {
        private readonly TaskCompletionSource<bool> _inner;

        public TaskCompletionSource() => _inner = new TaskCompletionSource<bool>();

        public TaskCompletionSource(TaskCreationOptions options) => _inner = new TaskCompletionSource<bool>(options);

        public Task Task => _inner.Task;

        public void SetResult() => _inner.SetResult(true);

        public bool TrySetResult() => _inner.TrySetResult(true);

        public void SetException(Exception e) => _inner.SetException(e);

        public bool TrySetException(Exception e) => _inner.TrySetException(e);

        public bool TrySetCanceled() => _inner.TrySetCanceled();
    }
}
#endif
