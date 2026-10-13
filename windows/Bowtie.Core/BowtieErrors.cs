namespace Bowtie.Core;

/// <summary>Viewer-facing error taxonomy mapped from HTTP and network failures.</summary>
public abstract class BowtieException : Exception
{
    protected BowtieException(string message, Exception? inner = null) : base(message, inner) { }
}

/// <summary>401 after a refresh attempt: the session is over; sign in again.</summary>
public sealed class UnauthorizedException : BowtieException
{
    public UnauthorizedException() : base("Unauthorized") { }
}

/// <summary>503 from POST /sessions: every tuner is busy.</summary>
public sealed class TunersBusyException : BowtieException
{
    public TunersBusyException(IReadOnlyList<ActiveSessionSummary> sessions, int otherInUse)
        : base("All tuners are in use")
    {
        Sessions = sessions;
        OtherInUse = otherInUse;
    }

    public IReadOnlyList<ActiveSessionSummary> Sessions { get; }

    /// <summary>Tuners held by other apps (e.g. Plex); 0 when none or from an older server.</summary>
    public int OtherInUse { get; }
}

/// <summary>422: codec/profile negotiation failed.</summary>
public sealed class NegotiationFailedException : BowtieException
{
    public NegotiationFailedException(string message) : base(message) { }
}

/// <summary>409 from scheduling a recording: more channels than tuners at that time.</summary>
public sealed class RecordingConflictException : BowtieException
{
    public RecordingConflictException(string message, int tunerCount, IReadOnlyList<Recording> conflicts)
        : base(message)
    {
        TunerCount = tunerCount;
        Conflicts = conflicts;
    }

    public int TunerCount { get; }
    public IReadOnlyList<Recording> Conflicts { get; }
}

/// <summary>404: unknown or disabled channel or resource.</summary>
public sealed class NotFoundException : BowtieException
{
    public NotFoundException() : base("Not found") { }
}

/// <summary>
/// Any other non-success status. <see cref="Exception.Message"/> is the
/// server's error text when <see cref="HasServerMessage"/>; otherwise the
/// status (and raw body) for logs, never shown to viewers.
/// </summary>
public sealed class ServerException : BowtieException
{
    public ServerException(int status, string message, bool hasServerMessage = true) : base(message)
    {
        Status = status;
        HasServerMessage = hasServerMessage;
    }

    public int Status { get; }

    /// <summary>The message is the server's own plain words (its JSON <c>error</c>).</summary>
    public bool HasServerMessage { get; }
}

/// <summary>Transport failure: no answer from the server.</summary>
public sealed class NetworkException : BowtieException
{
    public NetworkException(Exception inner) : base(inner.Message, inner) { }
}

/// <summary>
/// Plain-words error copy shared by the screens. Raw exception, HTTP and
/// player details never reach viewers; the server's own messages do.
/// </summary>
public static class ErrorText
{
    public const string CantReachServer = "Can't reach your Bowtie server. Check your connection and try again.";
    public const string SomethingWentWrong = "Something went wrong. Try again.";
    public const string StreamStopped = "The stream stopped. Try again.";
    public const string RecordingStopped = "The recording stopped. Go back and press Play again.";

    public static string For(Exception e) => e switch
    {
        UnauthorizedException => "Your session ended. Sign in again.",
        TunersBusyException => "All tuners are in use",
        RecordingConflictException => "Not enough tuners then",
        NegotiationFailedException n => n.Message,
        NotFoundException => "Not found",
        ServerException { HasServerMessage: true } s => s.Message,
        NetworkException => CantReachServer,
        _ => SomethingWentWrong,
    };
}
