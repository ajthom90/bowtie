using System.ComponentModel;
using System.Runtime.CompilerServices;

namespace Bowtie.Core.ViewModels;

/// <summary>
/// Minimal <see cref="INotifyPropertyChanged"/> base. View models raise
/// changes on the thread that resumed the async work; the app calls them on
/// the UI thread (continuations resume there), the client itself uses
/// <c>ConfigureAwait(false)</c>.
/// </summary>
public abstract class ObservableObject : INotifyPropertyChanged
{
    public event PropertyChangedEventHandler? PropertyChanged;

    protected bool SetProperty<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return false;
        field = value;
        OnPropertyChanged(name);
        return true;
    }

    protected void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
